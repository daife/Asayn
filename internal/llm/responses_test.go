package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
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

func TestSubscriptionRequestConstraints(t *testing.T) {
	c := NewClient(config.ProviderConfig{APIType: "subscription_responses", BaseURL: "https://untrusted.invalid", User: "user"})
	if c.apiURL != "https://api.openai.com/v1/responses" {
		t.Fatal("subscription endpoint must be public OpenAI")
	}
	body := c.buildRequest("model", []types.ChatMessage{{Role: "system", Content: "instructions"}, {Role: "user", Content: "hello"}}, nil, false, "", false).(map[string]any)
	if body["store"] != false || body["stream"] != true || body["input"].([]any)[0].(map[string]any)["role"] != "developer" {
		t.Fatalf("invalid subscription request: %v", body)
	}
	for _, key := range []string{"previous_response_id", "temperature", "max_output_tokens", "user", "metadata", "conversation"} {
		if _, exists := body[key]; exists {
			t.Errorf("unsupported field %s", key)
		}
	}
}

func TestSubscriptionTerminalErrors(t *testing.T) {
	c := NewClient(config.ProviderConfig{APIType: "subscription_responses"})
	for _, payload := range []string{
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"Plan limit reached"}}}`,
		`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"limit"}}}`,
	} {
		_, _, err := c.readResponsesStream(io.NopCloser(strings.NewReader("data: "+payload+"\n\n")), nil)
		if err == nil {
			t.Fatal("non-completed response accepted")
		}
		if strings.Contains(payload, "subscription_sharing_usage_limit_exceeded") && !strings.Contains(err.Error(), "subscription_sharing_usage_limit_exceeded") {
			t.Fatal("lost subscription error code")
		}
	}
}

func TestSubscriptionChatUsesAuthenticatedStream(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".Asayn", "subscription")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	session, _ := json.Marshal(map[string]any{"client_id": "issued", "scope": "chatgpt.tokens.use.direct", "access_token": "oauth-access", "expires_at": time.Now().Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(dir, "default.json"), session, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-access" {
			t.Error("OAuth token missing")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true || body["store"] != false {
			t.Error("invalid subscription options")
		}
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", responsesFixture)
	}))
	defer server.Close()
	c := NewClient(config.ProviderConfig{APIType: "subscription_responses"})
	c.apiURL = server.URL
	msg, _, err := c.Chat(context.Background(), "model", []types.ChatMessage{{Role: "user", Content: "hello"}}, nil, false, "")
	if err != nil || len(msg.ToolCalls) != 1 {
		t.Fatalf("nonstream caller failed: %+v %v", msg, err)
	}
}

func TestResponsesTerminalEnvelopePreservesStreamedOutput(t *testing.T) {
	for _, apiType := range []string{"responses", "subscription_responses"} {
		for _, terminal := range []string{
			`{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`,
			`{"type":"response.completed","response":{"status":"completed"}}`,
			`{"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}}`,
			`{"type":"response.incomplete","response":{"status":"incomplete"}}`,
			`{"type":"response.completed","response":null}`,
		} {
			for _, itemDone := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/done=%v", apiType, terminal, itemDone), func(t *testing.T) {
					c := NewClient(config.ProviderConfig{APIType: apiType})
					stream := "data: " + `{"type":"response.output_text.delta","output_index":0,"delta":"hello"}` + "\n\n"
					if itemDone {
						stream += "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"hello"}]}}` + "\n\n"
					}
					stream += "data: " + terminal + "\n\n"
					var displayed string
					msg, _, err := c.readResponsesStream(io.NopCloser(strings.NewReader(stream)), func(d StreamDelta) { displayed += d.Content })
					wantError := !strings.Contains(terminal, `"status":"completed"`)
					if (err != nil) != wantError {
						t.Fatalf("terminal error = %v", err)
					}
					if msg.Content != displayed || msg.Content != "hello" {
						t.Fatalf("visible output lost: displayed=%q saved=%q", displayed, msg.Content)
					}
					if err != nil && (streamError(err) == nil || streamError(err).Partial.Content != "hello") {
						t.Fatalf("partial reply lost on error: %v", err)
					}
				})
			}
		}
	}
}

func TestResponsesItemAssemblyOrderedWithoutDuplicates(t *testing.T) {
	events := []string{
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"thinking"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","encrypted_content":"opaque","summary":[]}}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"first"}`,
		`{"type":"response.output_text.delta","output_index":2,"delta":"second"}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"message","content":[{"type":"output_text","text":"second"}]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"first"}]}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"first"}]}}`,
		`{"type":"response.output_item.done","output_index":3,"item":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}}`,
		`{"type":"response.completed","response":{"status":"completed","output":[]}}`,
	}
	var stream strings.Builder
	for _, event := range events {
		fmt.Fprintf(&stream, "data: %s\n\n", event)
	}
	c := NewClient(config.ProviderConfig{APIType: "subscription_responses"})
	msg, _, err := c.readResponsesStream(io.NopCloser(strings.NewReader(stream.String())), nil)
	if err != nil || msg.Content != "firstsecond" || msg.ReasoningContent != "thinking" || len(msg.ResponsesReasoning) != 1 || len(msg.ToolCalls) != 1 {
		t.Fatalf("incorrect item assembly: %+v %v", msg, err)
	}
}

func TestResponsesReplySurvivesAgentSnapshotAndNextTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".Asayn", "subscription")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	credentials, _ := json.Marshal(map[string]any{"client_id": "issued", "scope": "chatgpt.tokens.use.direct", "access_token": "oauth-access", "expires_at": time.Now().Add(time.Hour)})
	if err := os.WriteFile(filepath.Join(dir, "default.json"), credentials, 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.Header.Get("session-id") == "" || request["prompt_cache_key"] != r.Header.Get("session-id") {
			t.Error("subscription request lost persisted session routing")
		}
		if requests == 2 {
			input := request["input"].([]any)
			if len(input) != 4 || input[2].(map[string]any)["content"] != "visible reply" {
				t.Error("next turn lost previous assistant reply")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","output_index":0,"delta":"visible reply"}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"visible reply"}]}}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
	}))
	defer server.Close()
	paths := config.Paths{WorkspaceRoot: home, WorkspaceDir: filepath.Join(home, ".Asayn"), HomeDir: filepath.Join(home, ".Asayn")}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sess, err := store.New("test", "default")
	if err != nil {
		t.Fatal(err)
	}
	agent := NewAgent(config.APIConfig{Providers: map[string]config.ProviderConfig{"ChatGPT": {APIType: "subscription_responses"}}}, config.AgentConfig{Provider: "ChatGPT", Model: "model"}, paths, tools.NewBasicExecutor(paths, store, 2000, false))
	agent.client.apiURL = server.URL
	for turn := 0; turn < 2; turn++ {
		var visible string
		answer, _, err := agent.AskWithEvents(context.Background(), sess, "hello", func(e AgentEvent) {
			if e.Kind == "assistant_delta" {
				visible += e.Text
			}
		})
		if err != nil || answer != visible || answer != "visible reply" {
			t.Fatalf("stream/final mismatch: visible=%q answer=%q error=%v", visible, answer, err)
		}
		if err = store.Save(sess); err != nil {
			t.Fatal(err)
		}
		sess, err = store.LoadByID(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if last := sess.Messages[len(sess.Messages)-1]; last.Content != visible {
			t.Fatalf("snapshot lost visible reply: %+v", last)
		}
	}
}
