package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/asayn/asayn/internal/tools"
)

type Agent struct {
	client *Client
	root   config.AgentConfig
	paths  config.Paths
	tools  *tools.Executor
	isSubAgent bool
}

func NewAgent(api config.APIConfig, root config.AgentConfig, paths config.Paths, executor *tools.Executor) *Agent {
	return &Agent{
		client: NewClient(api),
		root:   root,
		paths:  paths,
		tools:  executor,
	}
}

func NewSubAgent(api config.APIConfig, root config.AgentConfig, paths config.Paths, executor *tools.Executor) *Agent {
	agent := NewAgent(api, root, paths, executor)
	agent.isSubAgent = true
	return agent
}

func (a *Agent) Ask(ctx context.Context, sess *session.Session, prompt string) (string, error) {
	if len(sess.Messages) == 0 {
		sess.Messages = append(sess.Messages, types.ChatMessage{
			Role:    "system",
			Content: a.systemPrompt(sess),
		})
	}
	sess.Messages = append(sess.Messages, types.ChatMessage{Role: "user", Content: prompt})

	toolSchemas := a.tools.Schemas(a.isSubAgent)
	for step := 0; step < 12; step++ {
		msg, err := a.client.Chat(ctx, sess.Messages, toolSchemas)
		if err != nil {
			return "", err
		}
		sess.Messages = append(sess.Messages, msg)
		if len(msg.ToolCalls) == 0 {
			return msg.Content, nil
		}
		for _, call := range msg.ToolCalls {
			result := a.runToolCall(sess, call)
			sess.Messages = append(sess.Messages, types.ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}
	return "", fmt.Errorf("agent exceeded tool-call loop limit")
}

func (a *Agent) systemPrompt(sess *session.Session) string {
	prompt := a.root.SystemPrompt
	skills, err := config.ListSkills(a.paths)
	if err != nil || len(skills) == 0 {
		return prompt
	}
	visible := map[string]bool{}
	for _, name := range a.root.VisibleSkills {
		visible[name] = true
	}
	for name, enabled := range sess.VisibleSkills {
		visible[name] = enabled
	}
	blocks := []string{}
	for _, skill := range skills {
		if !visible[skill.Name] {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("<skill name=%q source=%q>\n%s\n</skill>", skill.Name, skill.Source, strings.TrimSpace(skill.Body)))
	}
	if len(blocks) == 0 {
		return prompt
	}
	return prompt + "\n\nVisible skills:\n" + strings.Join(blocks, "\n\n")
}

func (a *Agent) runToolCall(sess *session.Session, call types.ToolCall) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("tool argument JSON error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := a.tools.Run(ctx, sess, call.Function.Name, args)
	if err != nil {
		return fmt.Sprintf("tool error: %v", err)
	}
	return out
}
