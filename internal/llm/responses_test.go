package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

const responsesFixture = `{"status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"},{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"command\":\"pwd\"}"}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":80}}}`

func TestResponsesToolContinuation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer key" {
					t.Error("incorrect endpoint or auth")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["prompt_cache_key"] != "shared" || body["store"] != false || body["stream"] != streaming {
					t.Errorf("incorrect options: %v", body)
				}
				if _, ok := body["previous_response_id"]; ok {
					t.Error("unexpected server conversation")
				}
				tools := body["tools"].([]any)
				if tools[0].(map[string]any)["name"] != "shell" {
					t.Error("missing flattened tool")
				}
				if requests == 2 {
					input := body["input"].([]any)
					if input[1].(map[string]any)["encrypted_content"] != "opaque" || input[2].(map[string]any)["call_id"] != "call_1" || input[3].(map[string]any)["type"] != "function_call_output" {
						t.Errorf("invalid continuation: %v", input)
					}
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", responsesFixture)
				} else {
					io.WriteString(w, responsesFixture)
				}
			}))
			defer server.Close()
			c := NewClient(config.ProviderConfig{BaseURL: server.URL + "/v1/chat/completions", APIType: "responses", APIKey: "key", PromptCacheKey: "shared"})
			messages := []types.ChatMessage{{Role: "user", Content: "hello"}}
			tools := []types.ToolSchema{{Type: "function", Function: types.FunctionSchema{Name: "shell", Parameters: map[string]any{"type": "object"}}}}
			for i := 0; i < 2; i++ {
				var msg types.ChatMessage
				var usage types.Usage
				var err error
				if streaming {
					msg, usage, err = c.ChatStream(context.Background(), "model", messages, tools, true, "max", nil)
				} else {
					msg, usage, err = c.Chat(context.Background(), "model", messages, tools, true, "max")
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" || usage.PromptCacheHitTokens != 80 || usage.PromptCacheMissTokens != 20 {
					t.Fatalf("invalid result: %+v %+v", msg, usage)
				}
				// Simulate persisting and restoring the local session.
				stored, _ := json.Marshal(msg)
				var restored types.ChatMessage
				if err := json.Unmarshal(stored, &restored); err != nil {
					t.Fatal(err)
				}
				messages = append(messages, restored, types.ChatMessage{Role: "tool", ToolCallID: "call_1", Content: "ok"})
			}
		})
	}
}

func TestResponsesStreamPartialAndCompletion(t *testing.T) {
	c := NewClient(config.ProviderConfig{APIType: "responses"})
	prefix := "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"hello\"}\n\n"
	msg, _, err := c.readResponsesStream(io.NopCloser(strings.NewReader(prefix)), nil)
	if err == nil || msg.Content != "hello" {
		t.Fatalf("truncated stream lost progress: %+v %v", msg, err)
	}
	completed := prefix + "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}]}}\n\n"
	var text string
	msg, _, err = c.readResponsesStream(io.NopCloser(strings.NewReader(completed)), func(d StreamDelta) { text += d.Content })
	if err != nil || text != "hello" || msg.Content != "hello" {
		t.Fatalf("incorrect stream: %+v %q %v", msg, text, err)
	}
}

func TestChatRequestDoesNotLeakResponsesMetadata(t *testing.T) {
	c := NewClient(config.ProviderConfig{User: "shared-user"})
	data, err := json.Marshal(c.buildRequest("model", []types.ChatMessage{{Role: "assistant", ResponsesReasoning: []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)}}}, nil, false, "", false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "responses_reasoning") || !strings.Contains(string(data), `"user":"shared-user"`) {
		t.Fatalf("invalid chat request: %s", data)
	}
}
