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
	cfg     config.ProviderConfig
	http    *http.Client
	apiURL  string
	timeout time.Duration
}

const rateLimitMaxRetries = 10

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
	Usage types.Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type StreamDelta struct {
	ReasoningContent string
	Content          string
	Usage            *types.Usage
}

type streamResponse struct {
	Choices []struct {
		Delta        streamChoiceDelta `json:"delta"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Usage *types.Usage `json:"usage"`
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

func NewClient(cfg config.ProviderConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		cfg:     cfg,
		http:    &http.Client{},
		apiURL:  completionsURL(cfg.BaseURL),
		timeout: timeout,
	}
}

func (c *Client) Chat(ctx context.Context, model string, messages []types.ChatMessage, tools []types.ToolSchema, thinkingEnabled bool, reasoningEffort string) (types.ChatMessage, types.Usage, error) {
	ctx, cancel := contextWithTimeoutIfSooner(ctx, c.timeout)
	defer cancel()

	reqBody := buildChatRequest(model, messages, tools, thinkingEnabled, reasoningEffort, false)

	data, err := json.Marshal(reqBody)
	if err != nil {
		return types.ChatMessage{}, types.Usage{}, err
	}

	maxRetries := rateLimitMaxRetries
	baseWait := 1 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(data))
		if err != nil {
			return types.ChatMessage{}, types.Usage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return types.ChatMessage{}, types.Usage{}, err
			}
			return types.ChatMessage{}, types.Usage{}, err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return types.ChatMessage{}, types.Usage{}, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt == maxRetries {
				return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API rate limit exceeded after %d retries", maxRetries)
			}
			wait := baseWait * (1 << attempt)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return types.ChatMessage{}, types.Usage{}, ctx.Err()
			}
		}

		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("decode API response: %w: %s", err, string(body))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if parsed.Error != nil {
				return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, parsed.Error.Message)
			}
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
		}
		if len(parsed.Choices) == 0 {
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API returned no choices")
		}
		return parsed.Choices[0].Message, parsed.Usage, nil
	}
	return types.ChatMessage{}, types.Usage{}, fmt.Errorf("unreachable code")
}

func (c *Client) ChatStream(ctx context.Context, model string, messages []types.ChatMessage, tools []types.ToolSchema, thinkingEnabled bool, reasoningEffort string, onDelta func(StreamDelta)) (types.ChatMessage, types.Usage, error) {
	reqBody := buildChatRequest(model, messages, tools, thinkingEnabled, reasoningEffort, true)

	data, err := json.Marshal(reqBody)
	if err != nil {
		return types.ChatMessage{}, types.Usage{}, err
	}

	maxRetries := rateLimitMaxRetries
	baseWait := 1 * time.Second

	var resp *http.Response
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(data))
		if err != nil {
			return types.ChatMessage{}, types.Usage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}

		resp, err = c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return types.ChatMessage{}, types.Usage{}, err
			}
			return types.ChatMessage{}, types.Usage{}, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt == maxRetries {
				return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API rate limit exceeded after %d retries", maxRetries)
			}
			wait := baseWait * (1 << attempt)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return types.ChatMessage{}, types.Usage{}, ctx.Err()
			}
		}
		break
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	msg := types.ChatMessage{Role: "assistant"}
	var finalUsage types.Usage
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
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("decode stream chunk: %w: %s", err, payload)
		}
		if chunk.Error != nil {
			return types.ChatMessage{}, types.Usage{}, fmt.Errorf("API stream error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			finalUsage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			if onDelta != nil && chunk.Usage != nil {
				onDelta(StreamDelta{Usage: chunk.Usage})
			}
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
		if onDelta != nil && (delta.ReasoningContent != "" || delta.Content != "" || chunk.Usage != nil) {
			onDelta(StreamDelta{ReasoningContent: delta.ReasoningContent, Content: delta.Content, Usage: chunk.Usage})
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
		return types.ChatMessage{}, types.Usage{}, err
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
	return msg, finalUsage, nil
}

func contextWithTimeoutIfSooner(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
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
