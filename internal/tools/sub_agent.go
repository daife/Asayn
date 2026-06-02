package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SubAgentManager struct {
	limit int
	mu    sync.Mutex
	items map[string]*SubAgentTask
	runner SubAgentRunner
}

type SubAgentRunner func(ctx context.Context, taskID, name, instruction string) string

type SubAgentTask struct {
	ID         string
	Name       string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Transcript []string
	Result     string
	cancel     context.CancelFunc
	stop       chan struct{}
}

type SubAgentSnapshot struct {
	ID     string
	Name   string
	Status string
	Result string
}

func NewSubAgentManager(limit int) *SubAgentManager {
	return &SubAgentManager{limit: limit, items: map[string]*SubAgentTask{}}
}

func (m *SubAgentManager) SetRunner(runner SubAgentRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runner = runner
}

func (m *SubAgentManager) Start(name, instruction string) string {
	if name == "" {
		name = "sub-agent"
	}
	task := &SubAgentTask{
		ID:         uuid.NewString(),
		Name:       name,
		Status:     "running",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Transcript: []string{"user: " + instruction},
		stop:       make(chan struct{}),
	}
	m.mu.Lock()
	m.items[task.ID] = task
	m.mu.Unlock()

	go m.fakeRun(task, instruction)
	return fmt.Sprintf("sub_agent_id=%s\nstarted", task.ID)
}

func (m *SubAgentManager) Send(id, instruction string) string {
	m.mu.Lock()
	task := m.items[id]
	if task == nil {
		m.mu.Unlock()
		return "sub-agent not found"
	}
	if task.Status == "stopped" {
		task.stop = make(chan struct{})
	}
	task.Status = "running"
	task.UpdatedAt = time.Now()
	task.Transcript = append(task.Transcript, "user: "+instruction)
	m.mu.Unlock()
	go m.fakeRun(task, instruction)
	return "sent"
}

func (m *SubAgentManager) Status(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" {
		task := m.items[id]
		if task == nil {
			return "sub-agent not found"
		}
		return truncate(m.describe(task), m.limit)
	}
	rows := []string{}
	for _, task := range m.items {
		rows = append(rows, fmt.Sprintf("%s %s %s %s", task.ID, task.Status, task.Name, task.Result))
	}
	if len(rows) == 0 {
		return "no sub-agents"
	}
	return truncate(strings.Join(rows, "\n"), m.limit)
}

func (m *SubAgentManager) Stop(id string) string {
	m.mu.Lock()
	task := m.items[id]
	if task == nil {
		m.mu.Unlock()
		return "sub-agent not found"
	}
	task.Status = "stopped"
	task.UpdatedAt = time.Now()
	if task.cancel != nil {
		task.cancel()
	}
	select {
	case <-task.stop:
	default:
		close(task.stop)
	}
	m.mu.Unlock()
	return "stopped"
}

func (m *SubAgentManager) Summaries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for _, task := range m.items {
		out = append(out, fmt.Sprintf("%s: %s", task.Name, task.Status))
	}
	return out
}

func (m *SubAgentManager) Snapshots() []SubAgentSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SubAgentSnapshot{}
	for _, task := range m.items {
		out = append(out, SubAgentSnapshot{
			ID:     task.ID,
			Name:   task.Name,
			Status: task.Status,
			Result: task.Result,
		})
	}
	return out
}

func (m *SubAgentManager) fakeRun(task *SubAgentTask, instruction string) {
	m.mu.Lock()
	runner := m.runner
	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel
	m.mu.Unlock()
	defer cancel()

	select {
	case <-time.After(10 * time.Millisecond):
		result := "Sub-agent runner is not configured."
		if runner != nil {
			result = runner(ctx, task.ID, task.Name, instruction)
		}
		m.mu.Lock()
		if ctx.Err() != nil || task.Status == "stopped" {
			task.Status = "stopped"
			task.UpdatedAt = time.Now()
			m.mu.Unlock()
			return
		}
		task.Status = "completed"
		task.UpdatedAt = time.Now()
		task.Result = result
		task.Transcript = append(task.Transcript, "assistant: "+task.Result)
		m.mu.Unlock()
	case <-task.stop:
		return
	}
}

func (m *SubAgentManager) describe(task *SubAgentTask) string {
	return fmt.Sprintf("id: %s\nname: %s\nstatus: %s\nupdated: %s\nresult: %s\n\n%s",
		task.ID,
		task.Name,
		task.Status,
		task.UpdatedAt.Format(time.RFC3339),
		task.Result,
		strings.Join(task.Transcript, "\n"),
	)
}
