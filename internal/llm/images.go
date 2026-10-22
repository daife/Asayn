package llm

import (
	"encoding/json"

	"github.com/asayn/asayn/internal/llm/types"
)

// Image attachments remain on tool messages in local history so context hiding
// and turn counting keep their existing semantics. On the wire, collect them
// after the entire tool-output batch; Chat Completions tool outputs are text.
func imageMessages(messages []types.ChatMessage) []types.ChatMessage {
	out := make([]types.ChatMessage, 0, len(messages))
	var pending []types.ImageInput
	flush := func() {
		if len(pending) > 0 {
			out = append(out, types.ChatMessage{Role: "user", Content: "Images from the preceding tool results:", Images: pending})
			pending = nil
		}
	}
	for _, msg := range messages {
		if msg.Role != "tool" {
			flush()
		}
		if msg.Role == "tool" {
			pending = append(pending, msg.Images...)
			msg.Images = nil
		}
		out = append(out, msg)
	}
	flush()
	return out
}

func imageContent(msg types.ChatMessage, responses bool) []any {
	textType, imageType := "text", "image_url"
	if responses {
		textType, imageType = "input_text", "input_image"
	}
	parts := []any{}
	if msg.Content != "" {
		parts = append(parts, map[string]any{"type": textType, "text": msg.Content})
	}
	for _, image := range msg.Images {
		detail := image.Detail
		if detail == "" {
			detail = "auto"
		}
		if responses {
			parts = append(parts, map[string]any{"type": imageType, "image_url": image.URL, "detail": detail})
		} else {
			parts = append(parts, map[string]any{"type": imageType, "image_url": map[string]any{"url": image.URL, "detail": detail}})
		}
	}
	return parts
}

func (r chatRequest) MarshalJSON() ([]byte, error) {
	type plain chatRequest
	data, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	hasImages := false
	for _, msg := range r.Messages {
		if len(msg.Images) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return data, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	messages := []json.RawMessage{}
	for _, msg := range imageMessages(r.Messages) {
		images := msg.Images
		msg.Images = nil
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		if len(images) > 0 {
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				return nil, err
			}
			msg.Images = images
			wire["content"] = imageContent(msg, false)
			raw, err = json.Marshal(wire)
			if err != nil {
				return nil, err
			}
		}
		messages = append(messages, raw)
	}
	body["messages"], err = json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	return json.Marshal(body)
}
