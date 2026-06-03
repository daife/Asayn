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
		Transcript: []string{rootInstructionLine(instruction)},
		parent:     parent,
		store:      store,
	}
	m.mu.Lock()
	m.items[task.ID] = task
	m.mu.Unlock()
	task.persist()

	go m.run(task, instruction)
	return fmt.Sprintf("sub_agent_id=%s\nstatus: running\nContinue useful work and use sub_agent_check when this sub-agent becomes ready_for_check.", task.ID)
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
	task.Status = "running"
	task.UpdatedAt = time.Now()
	task.Transcript = append(task.Transcript, rootInstructionLine(instruction))
	task.persistLocked()
	m.mu.Unlock()
	go m.run(task, instruction)
	return fmt.Sprintf("sub_agent_id=%s\nstatus: running\nContinue useful work and use sub_agent_check when this sub-agent becomes ready_for_check.", task.ID)
}

func (m *SubAgentManager) List(paths config.Paths) string {
	rows := []string{"configured sub-agents:"}
	if infos, err := config.ListAgentInfos(paths, config.SubAgentKind); err == nil && len(infos) > 0 {
		for _, info := range infos {
			rows = append(rows, fmt.Sprintf("- %s: %s", info.Name, info.Description))
		}
	} else {
		rows = append(rows, "none")
	}
	return truncate(strings.Join(rows, "\n"), m.limit)
}

func (m *SubAgentManager) Check(id string) string {
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

func (m *SubAgentManager) WaitCheck(ctx context.Context, id string, waitSeconds int) (string, error) {
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
		return m.Check(id), nil
	}
}

func (m *SubAgentManager) StopAll() {
	m.mu.Lock()
	tasks := make([]*SubAgentTask, 0, len(m.items))
	for _, task := range m.items {
		tasks = append(tasks, task)
	}
	m.items = map[string]*SubAgentTask{}
	m.mu.Unlock()

	for _, task := range tasks {
		if task.cancel != nil {
			task.cancel()
		}
	}
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
		if ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		task.Status = "ready_for_check"
		if strings.HasPrefix(result, "sub-agent error:") {
			task.Status = "failed"
		}
		task.UpdatedAt = time.Now()
		task.Result = result
		task.Transcript = append(task.Transcript, subAgentAnswerLine(task.Name, task.Result))
		task.persistLocked()
		m.mu.Unlock()
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
		status := normalizeRestoredSubAgentStatus(ref.Status)
		if status == "" {
			continue
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
				task.Transcript = transcriptFromSession(sess, task.Name)
				task.Result = lastAssistantContent(sess)
			}
		}
		m.items[task.ID] = task
	}
}

func normalizeRestoredSubAgentStatus(status string) string {
	switch status {
	case "ready_for_check", "completed", "failed":
		return status
	default:
		return "completed"
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

func transcriptFromSession(sess *session.Session, subAgentName string) []string {
	out := []string{}
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "user":
			out = append(out, rootInstructionLine(msg.Content))
		case "assistant":
			if msg.Content != "" {
				out = append(out, subAgentAnswerLine(subAgentName, msg.Content))
			}
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

func (m *SubAgentManager) describeForRoot(task *SubAgentTask) string {
	lines := []string{
		fmt.Sprintf("id: %s", task.ID),
		fmt.Sprintf("name: %s", task.Name),
		fmt.Sprintf("status: %s", task.Status),
		fmt.Sprintf("updated: %s", task.UpdatedAt.Format(time.RFC3339)),
	}
	conversation := rootVisibleTranscript(task.Transcript, task.Name, task.Result)
	if len(conversation) > 0 {
		lines = append(lines, conversation...)
	}
	if task.Status == "running" {
		lines = append(lines, "", "This sub-agent is still running. Continue useful work; use sub_agent_wait_check only for one deliberate wait when there is truly no useful work to do.")
	}
	return strings.Join(lines, "\n")
}

func rootInstructionLine(instruction string) string {
	return "[root_agent]: " + strings.TrimSpace(instruction)
}

func subAgentAnswerLine(name, answer string) string {
	if name == "" {
		name = "sub_agent"
	}
	return fmt.Sprintf("[%s]: %s", name, strings.TrimSpace(answer))
}

func rootVisibleTranscript(transcript []string, name, result string) []string {
	lines := []string{}
	hasFinalAnswer := false
	for _, line := range transcript {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[root_agent]:"):
			lines = append(lines, line)
		case strings.HasPrefix(line, "[") && strings.Contains(line, "]:"):
			lines = append(lines, line)
			if !strings.HasPrefix(line, "[root_agent]:") {
				hasFinalAnswer = true
			}
		case strings.HasPrefix(line, "user:"):
			lines = append(lines, rootInstructionLine(strings.TrimPrefix(line, "user:")))
		}
	}
	if strings.TrimSpace(result) != "" && !hasFinalAnswer {
		lines = append(lines, subAgentAnswerLine(name, result))
	}
	return lines
}
