package llm

import (
	"encoding/json"
	"testing"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

func TestImageToolWireFormatsAndHistory(t *testing.T) {
	images := []types.ImageInput{{URL: "data:image/png;base64,aGVsbG8=", Detail: "high"}}
	history := []types.ChatMessage{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", ToolCalls: []types.ToolCall{{ID: "a", Type: "function", Function: types.ToolFunction{Name: "view_image", Arguments: `{"path":"test.png"}`}}, {ID: "b", Type: "function", Function: types.ToolFunction{Name: "file_read", Arguments: `{"path":"test.txt"}`}}}},
		{Role: "tool", ToolCallID: "a", Content: "Image loaded", Images: images},
		{Role: "tool", ToolCallID: "b", Content: "text"},
	}
	saved, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var restored []types.ChatMessage
	if err := json.Unmarshal(saved, &restored); err != nil {
		t.Fatal(err)
	}
	for _, apiType := range []string{"", "responses", "subscription_responses"} {
		t.Run(apiType, func(t *testing.T) {
			c := NewClient(config.ProviderConfig{APIType: apiType})
			data, err := json.Marshal(c.buildRequest("vision", restored, nil, false, "", false))
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			key := "messages"
			if apiType != "" {
				key = "input"
			}
			wire := body[key].([]any)
			// Responses expands the two assistant tool calls into separate items.
			last := wire[len(wire)-1].(map[string]any)
			if last["role"] != "user" {
				t.Fatalf("image inserted inside tool batch: %s", data)
			}
			parts := last["content"].([]any)
			image := parts[1].(map[string]any)
			if apiType == "" {
				if image["type"] != "image_url" || image["image_url"].(map[string]any)["url"] != images[0].URL {
					t.Fatalf("bad Chat image: %s", data)
				}
			} else {
				if image["type"] != "input_image" || image["image_url"] != images[0].URL || image["detail"] != "high" {
					t.Fatalf("bad Responses image: %s", data)
				}
			}
			if apiType == "subscription_responses" && (body["store"] != false || body["stream"] != true) {
				t.Fatal("subscription flags lost")
			}
		})
	}
	if restored[2].Images[0].URL != images[0].URL || latestUserTurn(restored) != 1 {
		t.Fatal("local history mutated")
	}
	stale := append(restored, types.ChatMessage{Role: "user", Content: "2"}, types.ChatMessage{Role: "user", Content: "3"}, types.ChatMessage{Role: "user", Content: "4"})
	clean := prepareMessagesForAPI(stale, false, true)
	if len(clean[2].Images) != 0 || len(stale[2].Images) != 1 {
		t.Fatal("stale images not hidden or history mutated")
	}
}
