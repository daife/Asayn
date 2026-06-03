package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

type Client struct {
	cfg    config.APIConfig
	http   *http.Client
	apiURL string
}

type chatRequest struct {
	Model           string              `json:"model"`
	Messages        []types.ChatMessage `json:"messages"`
	Tools           []types.ToolSchema  `json:"tools,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	Thinking        map[string]string   `json:"thinking,omitempty"`
	Stream          bool                `json:"stream,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message types.ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type StreamDelta struct {
	ReasoningContent string
	Content          string
}

type streamResponse struct {
	Choices []struct {
		Delta        streamChoiceDelta `json:"delta"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type streamChoiceDelta struct {
	Role             string                `json:"role,omitempty"`
	Content          string                `json:"content,omitempty"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
	ToolCalls        []streamToolCallDelta `json:"tool_calls,omitempty"`
}

type streamToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function streamToolFunctionDelta `json:"function,omitempty"`
}

type streamToolFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func NewClient(cfg config.APIConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		cfg:    cfg,
		http:   &http.Client{Timeout: timeout},
		apiURL: completionsURL(cfg.BaseURL),
	}
}

func (c *Client) Chat(ctx context.Context, model string, messages []types.ChatMessage, tools []types.ToolSchema, thinkingEnabled bool, reasoningEffort string) (types.ChatMessage, error) {
	reqBody := buildChatRequest(model, messages, tools, thinkingEnabled, reasoningEffort, false)

	data, err := json.Marshal(reqBody)
	if err != nil {
		return types.ChatMessage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(data))
	if err != nil {
		return types.ChatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return types.ChatMessage{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.ChatMessage{}, err
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return types.ChatMessage{}, fmt.Errorf("decode API response: %w: %s", err, string(body))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parsed.Error != nil {
			return types.ChatMessage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return types.ChatMessage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	if len(parsed.Choices) == 0 {
		return types.ChatMessage{}, fmt.Errorf("API returned no choices")
	}
	return parsed.Choices[0].Message, nil
}

func (c *Client) ChatStream(ctx context.Context, model string, messages []types.ChatMessage, tools []types.ToolSchema, thinkingEnabled bool, reasoningEffort string, onDelta func(StreamDelta)) (types.ChatMessage, error) {
	reqBody := buildChatRequest(model, messages, tools, thinkingEnabled, reasoningEffort, true)

	data, err := json.Marshal(reqBody)
	if err != nil {
		return types.ChatMessage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(data))
	if err != nil {
		return types.ChatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return types.ChatMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
			return types.ChatMessage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return types.ChatMessage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	msg := types.ChatMessage{Role: "assistant"}
	toolCalls := map[int]types.ToolCall{}
	maxToolIndex := -1
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return types.ChatMessage{}, fmt.Errorf("decode stream chunk: %w: %s", err, payload)
		}
		if chunk.Error != nil {
			return types.ChatMessage{}, fmt.Errorf("API stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Role != "" {
			msg.Role = delta.Role
		}
		if delta.ReasoningContent != "" {
			msg.ReasoningContent += delta.ReasoningContent
		}
		if delta.Content != "" {
			msg.Content += delta.Content
		}
		if onDelta != nil && (delta.ReasoningContent != "" || delta.Content != "") {
			onDelta(StreamDelta{ReasoningContent: delta.ReasoningContent, Content: delta.Content})
		}
		for _, part := range delta.ToolCalls {
			call := toolCalls[part.Index]
			if part.ID != "" {
				call.ID = part.ID
			}
			if part.Type != "" {
				call.Type = part.Type
			}
			if part.Function.Name != "" {
				call.Function.Name += part.Function.Name
			}
			if part.Function.Arguments != "" {
				call.Function.Arguments += part.Function.Arguments
			}
			toolCalls[part.Index] = call
			if part.Index > maxToolIndex {
				maxToolIndex = part.Index
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return types.ChatMessage{}, err
	}
	if maxToolIndex >= 0 {
		msg.ToolCalls = make([]types.ToolCall, 0, maxToolIndex+1)
		for i := 0; i <= maxToolIndex; i++ {
			call, ok := toolCalls[i]
			if !ok {
				continue
			}
			if call.Type == "" {
				call.Type = "function"
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		}
	}
	return msg, nil
}

func buildChatRequest(model string, messages []types.ChatMessage, tools []types.ToolSchema, thinkingEnabled bool, reasoningEffort string, stream bool) chatRequest {
	reqBody := chatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   stream,
	}
	if thinkingEnabled {
		reqBody.ReasoningEffort = normalizedReasoningEffort(reasoningEffort)
		reqBody.Thinking = map[string]string{"type": "enabled"}
	} else {
		reqBody.Thinking = map[string]string{"type": "disabled"}
	}
	return reqBody
}

func completionsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func normalizedReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "max", "xhigh":
		return "max"
	default:
		return "high"
	}
}
