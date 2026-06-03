package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/session"
	"github.com/google/uuid"
)

type SubAgentManager struct {
	limit  int
	mu     sync.Mutex
	items  map[string]*SubAgentTask
	runner SubAgentRunner
}

type SubAgentRunner func(ctx context.Context, taskID, sessionID, agentName, name, instruction string, emit func(string), bind func(string)) string

type SubAgentTask struct {
	ID         string
	Name       string
	Status     string
	Agent      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Transcript []string
	Result     string
	SessionID  string
	parent     *session.Session
	store      *session.Store
	cancel     context.CancelFunc
	stop       chan struct{}
}

type SubAgentSnapshot struct {
	ID         string
	Name       string
	Status     string
	Agent      string
	Result     string
	Transcript []string
	SessionID  string
}

func NewSubAgentManager(limit int) *SubAgentManager {
	return &SubAgentManager{limit: limit, items: map[string]*SubAgentTask{}}
}

func (m *SubAgentManager) SetLimit(limit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limit = limit
}

func (m *SubAgentManager) SetRunner(runner SubAgentRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runner = runner
}

func (m *SubAgentManager) Start(parent *session.Session, store *session.Store, agentName, name, instruction string) string {
	if agentName == "" {
		agentName = "default"
	}
	if name == "" {
		name = agentName
	}
	now := time.Now()
	task := &SubAgentTask{
		ID:         uuid.NewString(),
		Name:       name,
		Status:     "running",
		Agent:      agentName,
		CreatedAt:  now,
		UpdatedAt:  now,
		Transcript: []string{"user: " + instruction},
		parent:     parent,
		store:      store,
		stop:       make(chan struct{}),
	}
	m.mu.Lock()
	m.items[task.ID] = task
	m.mu.Unlock()
	task.persist()

	go m.run(task, instruction)
	return fmt.Sprintf("sub_agent_id=%s\nstatus: running (parallel — continue with other work and check back later with sub_agent_check)", task.ID)
}

func (m *SubAgentManager) ResumeAsync(id, instruction string) string {
	m.mu.Lock()
	task := m.items[id]
	if task == nil {
		m.mu.Unlock()
		return "sub-agent not found"
	}
	if task.Status == "running" {
		m.mu.Unlock()
		return "sub-agent is still running; wait for completion first"
	}
	if task.Status == "stopped" {
		task.stop = make(chan struct{})
	}
	task.Status = "running"
	task.UpdatedAt = time.Now()
	task.Transcript = append(task.Transcript, "user: "+instruction)
	task.persistLocked()
	m.mu.Unlock()
	go m.run(task, instruction)
	return fmt.Sprintf("sub_agent_id=%s\nstatus: running (parallel — continue with other work and check back later with sub_agent_check)", task.ID)
}

func (m *SubAgentManager) List(paths config.Paths) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []string{"available sub-agent configs:"}
	if infos, err := config.ListAgentInfos(paths, config.SubAgentKind); err == nil && len(infos) > 0 {
		for _, info := range infos {
			rows = append(rows, fmt.Sprintf("- %s [%s]: %s", info.Name, info.Source, info.Description))
		}
	} else {
		rows = append(rows, "none")
	}
	rows = append(rows, "", "active sub-agents:")
	for _, task := range m.items {
		rows = append(rows, fmt.Sprintf("%s %s agent=%s name=%s", task.ID, task.Status, task.Agent, task.Name))
	}
	if len(m.items) == 0 {
		rows = append(rows, "none")
	}
	return truncate(strings.Join(rows, "\n"), m.limit)
}

func (m *SubAgentManager) Check(paths config.Paths, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.items[id]
	if task == nil {
		return "status: notfound"
	}
	res := m.describeForRoot(task)
	if task.Status == "ready_for_check" {
		task.Status = "completed"
		task.UpdatedAt = time.Now()
		task.persistLocked()
	}
	return truncate(res, m.limit)
}

func (m *SubAgentManager) WaitCheck(ctx context.Context, paths config.Paths, id string, waitSeconds int) (string, error) {
	if id == "" {
		return "", fmt.Errorf("sub_agent_id is required")
	}
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	timer := time.NewTimer(time.Duration(waitSeconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return m.Check(paths, id), nil
	}
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
	task.persistLocked()
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

func (m *SubAgentManager) StopAll() {
	m.mu.Lock()
	tasks := make([]*SubAgentTask, 0, len(m.items))
	for _, task := range m.items {
		tasks = append(tasks, task)
		task.Status = "stopped"
		task.UpdatedAt = time.Now()
		task.persistLocked()
	}
	m.mu.Unlock()

	for _, task := range tasks {
		if task.cancel != nil {
			task.cancel()
		}
		select {
		case <-task.stop:
		default:
			close(task.stop)
		}
	}
}

func (m *SubAgentManager) Summaries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for _, task := range m.items {
		out = append(out, fmt.Sprintf("%s: %s", task.Name, task.Status))
	}
	sort.Strings(out)
	return out
}

func (m *SubAgentManager) Snapshots() []SubAgentSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SubAgentSnapshot{}
	for _, task := range m.items {
		out = append(out, SubAgentSnapshot{
			ID:         task.ID,
			Name:       task.Name,
			Status:     task.Status,
			Agent:      task.Agent,
			Result:     task.Result,
			Transcript: append([]string(nil), task.Transcript...),
			SessionID:  task.SessionID,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *SubAgentManager) run(task *SubAgentTask, instruction string) {
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
			result = runner(ctx, task.ID, task.SessionID, task.Agent, task.Name, instruction, func(line string) {
				m.mu.Lock()
				task.Transcript = append(task.Transcript, line)
				task.UpdatedAt = time.Now()
				m.mu.Unlock()
			}, func(sessionID string) {
				m.mu.Lock()
				task.SessionID = sessionID
				task.UpdatedAt = time.Now()
				task.persistLocked()
				m.mu.Unlock()
			})
		}
		m.mu.Lock()
		if ctx.Err() != nil || task.Status == "stopped" {
			task.Status = "stopped"
			task.UpdatedAt = time.Now()
			task.persistLocked()
			m.mu.Unlock()
			return
		}
		task.Status = "ready_for_check"
		if strings.HasPrefix(result, "sub-agent error:") {
			task.Status = "failed"
		}
		task.UpdatedAt = time.Now()
		task.Result = result
		task.Transcript = append(task.Transcript, "assistant: "+task.Result)
		task.persistLocked()
		m.mu.Unlock()
	case <-task.stop:
		return
	}
}

func (m *SubAgentManager) Restore(parent *session.Session, store *session.Store, refs []session.SubAgentRef, subStore *session.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = map[string]*SubAgentTask{}
	for _, ref := range refs {
		if ref.TaskID == "" {
			continue
		}
		status := ref.Status
		if status == "running" {
			status = "restored"
		}
		task := &SubAgentTask{
			ID:        ref.TaskID,
			Name:      ref.Name,
			Status:    status,
			Agent:     ref.Agent,
			CreatedAt: ref.CreatedAt,
			UpdatedAt: ref.UpdatedAt,
			SessionID: ref.SessionID,
			parent:    parent,
			store:     store,
			stop:      make(chan struct{}),
		}
		if task.Name == "" {
			task.Name = "sub-agent"
		}
		if task.Agent == "" {
			task.Agent = "default"
		}
		if task.CreatedAt.IsZero() {
			task.CreatedAt = time.Now()
		}
		if task.UpdatedAt.IsZero() {
			task.UpdatedAt = task.CreatedAt
		}
		if ref.SessionID != "" && subStore != nil {
			if sess, err := subStore.LoadByID(ref.SessionID); err == nil {
				task.Transcript = transcriptFromSession(sess)
				task.Result = lastAssistantContent(sess)
			}
		}
		if len(task.Transcript) == 0 {
			task.Transcript = []string{"restored sub-agent session"}
		}
		m.items[task.ID] = task
	}
}

func (t *SubAgentTask) persist() {
	t.persistLocked()
}

func (t *SubAgentTask) persistLocked() {
	if t.store == nil || t.parent == nil {
		return
	}
	_ = t.store.UpsertSubAgent(t.parent, session.SubAgentRef{
		TaskID:    t.ID,
		SessionID: t.SessionID,
		Name:      t.Name,
		Agent:     t.Agent,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	})
}

func transcriptFromSession(sess *session.Session) []string {
	out := []string{}
	toolNames := map[string]string{}
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "user":
			out = append(out, "user: "+msg.Content)
		case "assistant":
			if msg.ReasoningContent != "" {
				out = append(out, "thinking: "+msg.ReasoningContent)
			}
			if msg.Content != "" {
				out = append(out, "assistant: "+msg.Content)
			}
			for _, call := range msg.ToolCalls {
				toolNames[call.ID] = call.Function.Name
				out = append(out, fmt.Sprintf("tool: %s(%s)", call.Function.Name, call.Function.Arguments))
			}
		case "tool":
			name := toolNames[msg.ToolCallID]
			if name == "" {
				name = msg.ToolCallID
			}
			out = append(out, fmt.Sprintf("tool result: %s\n%s", name, msg.Content))
		}
	}
	return out
}

func lastAssistantContent(sess *session.Session) string {
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "assistant" && strings.TrimSpace(sess.Messages[i].Content) != "" {
			return sess.Messages[i].Content
		}
	}
	return ""
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

func (m *SubAgentManager) describeForRoot(task *SubAgentTask) string {
	lines := []string{
		fmt.Sprintf("id: %s", task.ID),
		fmt.Sprintf("name: %s", task.Name),
		fmt.Sprintf("status: %s", task.Status),
		fmt.Sprintf("updated: %s", task.UpdatedAt.Format(time.RFC3339)),
	}
	for _, line := range task.Transcript {
		if strings.HasPrefix(line, "assistant:") ||
			strings.HasPrefix(line, "tool:") ||
			strings.HasPrefix(line, "tool result:") ||
			strings.HasPrefix(line, "tool error:") {
			lines = append(lines, line)
		}
	}
	if task.Result != "" {
		lines = append(lines, "result: "+task.Result)
	}
	if task.Status == "running" {
		lines = append(lines, "", "This sub-agent is still running. Do not call shell tools for it. Continue with other worthwhile parallel work first; if none remains, use sub_agent_wait before checking again.")
	}
	return strings.Join(lines, "\n")
}
