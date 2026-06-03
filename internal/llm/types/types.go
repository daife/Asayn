package types

type ChatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	Name             string     `json:"name,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
	PromptTokens          int          `json:"prompt_tokens"`
	CompletionTokens      int          `json:"completion_tokens"`
	TotalTokens           int          `json:"total_tokens"`
	PromptCacheHitTokens  int          `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int          `json:"prompt_cache_miss_tokens"`
	Details               UsageDetails `json:"prompt_tokens_details"`
}

type UsageDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSchema struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
