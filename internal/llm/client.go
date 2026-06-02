package llm

import (
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

func (c *Client) Chat(ctx context.Context, messages []types.ChatMessage, tools []types.ToolSchema) (types.ChatMessage, error) {
	reqBody := chatRequest{
		Model:           c.cfg.Model,
		Messages:        messages,
		Tools:           tools,
		ReasoningEffort: c.cfg.ReasoningEffort,
	}
	if c.cfg.ThinkingEnabled {
		reqBody.Thinking = map[string]string{"type": "enabled"}
	} else {
		reqBody.Thinking = map[string]string{"type": "disabled"}
	}

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

func completionsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}
