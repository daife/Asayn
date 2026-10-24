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
	if cfg.APIType == "subscription_responses" {
		return "https://api.openai.com/v1/responses"
	}
	if cfg.APIType == "responses" {
		return base + "/responses"
	}
	return completionsURL(base)
}

func (c *Client) buildRequest(model string, messages []types.ChatMessage, tools []types.ToolSchema, thinking bool, effort string, stream bool) any {
	if !c.usesResponses() {
		r := buildChatRequest(model, messages, tools, thinking, effort, stream)
		r.User, r.PromptCacheKey = c.cfg.User, c.cfg.PromptCacheKey
		return r
	}
	input := []any{}
	for _, m := range imageMessages(messages) {
		if m.Role == "assistant" {
			for _, item := range m.ResponsesReasoning {
				if c.cfg.APIType == "subscription_responses" {
					// Subscription input rejects the output-only reasoning status.
					// Normalize on replay so persisted sessions work without migration.
					var reasoning map[string]json.RawMessage
					if json.Unmarshal(item, &reasoning) == nil && reasoning != nil {
						delete(reasoning, "status")
						input = append(input, reasoning)
						continue
					}
				}
				input = append(input, item)
			}
		}
		if m.Role == "tool" {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
			continue
		}
		if m.Content != "" || len(m.Images) > 0 || m.Role != "assistant" {
			role := m.Role
			if c.cfg.APIType == "subscription_responses" && role == "system" {
				role = "developer"
			}
			var content any = m.Content
			if len(m.Images) > 0 {
				content = imageContent(m, true)
			}
			input = append(input, map[string]any{"role": role, "content": content})
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
	if c.cfg.APIType == "subscription_responses" {
		r["stream"] = true
	}
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
	// Agent invocations supply their persisted per-session key. Standalone
	// clients retain the provider configuration fallback.
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
		Code    string `json:"code"`
		Param   string `json:"param"`
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
		return msg, usage, fmt.Errorf("Responses API %s (%s): %s", r.Error.Code, r.Error.Param, r.Error.Message)
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
	// Keep output items by index. Terminal envelopes may omit output already
	// delivered by item/delta events; they must never erase streamed progress.
	pending := map[int]StreamDelta{}
	items := map[int]types.ChatMessage{}
	merge := func(index int, snapshot types.ChatMessage) {
		previous := items[index]
		if snapshot.Content == "" {
			snapshot.Content = previous.Content
		}
		if snapshot.ReasoningContent == "" {
			snapshot.ReasoningContent = previous.ReasoningContent
		}
		if len(snapshot.ToolCalls) == 0 {
			snapshot.ToolCalls = previous.ToolCalls
		}
		if len(snapshot.ResponsesReasoning) == 0 {
			snapshot.ResponsesReasoning = previous.ResponsesReasoning
		}
		if snapshot.Content == "" {
			snapshot.Content = pending[index].Content
		}
		if snapshot.ReasoningContent == "" {
			snapshot.ReasoningContent = pending[index].ReasoningContent
		}
		items[index] = snapshot
		delete(pending, index)
	}
	collect := func() types.ChatMessage {
		indexes := map[int]bool{}
		for i := range items {
			indexes[i] = true
		}
		for i := range pending {
			indexes[i] = true
		}
		ordered := make([]int, 0, len(indexes))
		for i := range indexes {
			ordered = append(ordered, i)
		}
		sort.Ints(ordered)
		result := types.ChatMessage{Role: "assistant"}
		for _, i := range ordered {
			item := items[i]
			result.Content += item.Content + pending[i].Content
			result.ReasoningContent += item.ReasoningContent + pending[i].ReasoningContent
			result.ToolCalls = append(result.ToolCalls, item.ToolCalls...)
			result.ResponsesReasoning = append(result.ResponsesReasoning, item.ResponsesReasoning...)
		}
		return result
	}
	fail := func(err error) (types.ChatMessage, types.Usage, error) {
		msg = collect()
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
			var item types.ChatMessage
			if err := appendResponseItem(&item, event.Item); err != nil {
				return false, err
			}
			merge(event.OutputIndex, item)
		case "response.completed", "response.incomplete", "response.failed":
			var response responsesResult
			if err := json.Unmarshal(event.Response, &response); err != nil {
				return false, fmt.Errorf("decode Responses terminal event: %w", err)
			}
			for index, raw := range response.Output {
				var item types.ChatMessage
				if err := appendResponseItem(&item, raw); err != nil {
					return false, err
				}
				merge(index, item)
			}
			_, u, err := decodeResponses(event.Response)
			msg, usage = collect(), u
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
