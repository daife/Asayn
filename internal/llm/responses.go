package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

func providerURL(cfg config.ProviderConfig) string {
	base := strings.TrimRight(cfg.BaseURL, "/")
	for _, suffix := range []string{"/chat/completions", "/responses"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if cfg.APIType == "responses" {
		return base + "/responses"
	}
	return completionsURL(base)
}

func (c *Client) buildRequest(model string, messages []types.ChatMessage, tools []types.ToolSchema, thinking bool, effort string, stream bool) any {
	if c.cfg.APIType != "responses" {
		r := buildChatRequest(model, messages, tools, thinking, effort, stream)
		r.User, r.PromptCacheKey = c.cfg.User, c.cfg.PromptCacheKey
		return r
	}
	input := []any{}
	for _, m := range messages {
		if m.Role == "assistant" {
			for _, item := range m.ResponsesReasoning {
				input = append(input, item)
			}
		}
		if m.Role == "tool" {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
			continue
		}
		if m.Content != "" || m.Role != "assistant" {
			input = append(input, map[string]any{"role": m.Role, "content": m.Content})
		}
		for _, call := range m.ToolCalls {
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
		}
	}
	functions := []any{}
	for _, t := range tools {
		functions = append(functions, map[string]any{"type": "function", "name": t.Function.Name, "description": t.Function.Description, "parameters": t.Function.Parameters, "strict": false})
	}
	r := map[string]any{"model": model, "input": input, "store": false, "stream": stream, "include": []string{"reasoning.encrypted_content"}}
	if len(functions) > 0 {
		r["tools"] = functions
	}
	if thinking {
		effort = strings.ToLower(strings.TrimSpace(effort))
		if effort == "max" {
			effort = "xhigh"
		}
		if effort == "" {
			effort = "high"
		}
		r["reasoning"] = map[string]string{"effort": effort}
	}
	// A provider-wide key stays stable across sessions and subagents. Never use
	// session IDs or previous_response_id to partition shared prompt prefixes.
	key := c.cfg.PromptCacheKey
	if key == "" {
		key = c.cfg.User
	}
	if key != "" {
		r["prompt_cache_key"] = key
	}
	return r
}

type responseItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type responsesResult struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		Input   int                `json:"input_tokens"`
		Output  int                `json:"output_tokens"`
		Total   int                `json:"total_tokens"`
		Details types.UsageDetails `json:"input_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails any `json:"incomplete_details"`
}

func appendResponseItem(msg *types.ChatMessage, raw json.RawMessage) error {
	var item responseItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return err
	}
	switch item.Type {
	case "message":
		for _, part := range item.Content {
			msg.Content += part.Text + part.Refusal
		}
	case "function_call":
		msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{ID: item.CallID, Type: "function", Function: types.ToolFunction{Name: item.Name, Arguments: item.Arguments}})
	case "reasoning":
		msg.ResponsesReasoning = append(msg.ResponsesReasoning, append(json.RawMessage(nil), raw...))
		for _, part := range item.Summary {
			msg.ReasoningContent += part.Text
		}
	}
	return nil
}

func decodeResponses(data []byte) (types.ChatMessage, types.Usage, error) {
	var r responsesResult
	msg := types.ChatMessage{Role: "assistant"}
	if err := json.Unmarshal(data, &r); err != nil {
		return msg, types.Usage{}, fmt.Errorf("decode Responses API: %w", err)
	}
	usage := types.Usage{PromptTokens: r.Usage.Input, CompletionTokens: r.Usage.Output, TotalTokens: r.Usage.Total, Details: r.Usage.Details, PromptCacheHitTokens: r.Usage.Details.CachedTokens, PromptCacheMissTokens: r.Usage.Input - r.Usage.Details.CachedTokens}
	for _, raw := range r.Output {
		if err := appendResponseItem(&msg, raw); err != nil {
			return msg, usage, err
		}
	}
	if r.Error != nil {
		return msg, usage, fmt.Errorf("Responses API: %s", r.Error.Message)
	}
	if r.Status != "completed" {
		return msg, usage, fmt.Errorf("Responses API status %q: %v", r.Status, r.IncompleteDetails)
	}
	return msg, usage, nil
}

func (c *Client) readResponsesStream(body io.ReadCloser, onDelta func(StreamDelta)) (types.ChatMessage, types.Usage, error) {
	msg := types.ChatMessage{Role: "assistant"}
	var usage types.Usage
	idle := newStreamIdleTimer(body, c.timeout)
	defer idle.Stop()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	pending := map[int]StreamDelta{}
	fail := func(err error) (types.ChatMessage, types.Usage, error) {
		indexes := make([]int, 0, len(pending))
		for i := range pending {
			indexes = append(indexes, i)
		}
		sort.Ints(indexes)
		for _, i := range indexes {
			msg.Content += pending[i].Content
			msg.ReasoningContent += pending[i].ReasoningContent
		}
		return msg, usage, &StreamError{Message: err.Error(), Partial: msg, Usage: usage}
	}
	// SSE data may span multiple lines. Dispatch only once the event is complete.
	var data []string
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			return false, nil
		}
		var event struct {
			Type        string          `json:"type"`
			Delta       string          `json:"delta"`
			OutputIndex int             `json:"output_index"`
			Item        json.RawMessage `json:"item"`
			Response    json.RawMessage `json:"response"`
			Message     string          `json:"message"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return false, fmt.Errorf("decode Responses stream: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			part := pending[event.OutputIndex]
			part.Content += event.Delta
			pending[event.OutputIndex] = part
			if onDelta != nil {
				onDelta(StreamDelta{Content: event.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			part := pending[event.OutputIndex]
			part.ReasoningContent += event.Delta
			pending[event.OutputIndex] = part
			if onDelta != nil {
				onDelta(StreamDelta{ReasoningContent: event.Delta})
			}
		case "response.output_item.done":
			delete(pending, event.OutputIndex)
			if err := appendResponseItem(&msg, event.Item); err != nil {
				return false, err
			}
		case "response.completed", "response.incomplete", "response.failed":
			final, u, err := decodeResponses(event.Response)
			msg, usage = final, u
			pending = map[int]StreamDelta{}
			if onDelta != nil {
				onDelta(StreamDelta{Usage: &usage})
			}
			return true, err
		case "error":
			return false, fmt.Errorf("Responses stream: %s", event.Message)
		}
		return false, nil
	}
	for scanner.Scan() {
		idle.Reset()
		line := scanner.Text()
		if line == "" {
			done, err := dispatch()
			if err != nil {
				return fail(err)
			}
			if done {
				return msg, usage, nil
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if idle.TimedOut() {
		if onDelta != nil {
			onDelta(StreamDelta{Event: "timeout", Timeout: c.timeout, Message: "API stream idle timeout"})
		}
		return fail(fmt.Errorf("API stream idle timeout after %s", c.timeout))
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	done, err := dispatch()
	if err != nil {
		return fail(err)
	}
	if done {
		return msg, usage, nil
	}
	return fail(fmt.Errorf("Responses stream ended before response.completed"))
}
