package llm

import (
	"context"
	"strings"
	"sync"
)

// TurnInput collects steering messages without modifying the running session.
// Only the agent loop drains it, after all tool results have been recorded.
type TurnInput struct {
	mu      sync.Mutex
	pending []string
	closed  bool
}

func (i *TurnInput) Submit(text string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || strings.TrimSpace(text) == "" {
		return false
	}
	i.pending = append(i.pending, text)
	return true
}

func (i *TurnInput) drain(finish bool) []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	messages := i.pending
	i.pending = nil
	if finish && len(messages) == 0 {
		i.closed = true
	}
	return messages
}

type turnInputKey struct{}

func WithTurnInput(ctx context.Context, input *TurnInput) context.Context {
	return context.WithValue(ctx, turnInputKey{}, input)
}

// Pending returns a snapshot for displaying messages awaiting insertion.
func (i *TurnInput) Pending() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.pending...)
}
