package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/llm/types"
	"github.com/google/uuid"
)

type Session struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	RootAgent       string              `json:"root_agent"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Messages        []types.ChatMessage `json:"messages"`
	CompactedBefore int                 `json:"compacted_before,omitempty"`
	VisibleSkills   map[string]bool     `json:"visible_skills"`
	SubAgents       []SubAgentRef       `json:"sub_agents"`
	InputHistory    []string            `json:"input_history"`
	LastTotalTokens int                 `json:"last_total_tokens,omitempty"`
}

type SubAgentRef struct {
	TaskID    string    `json:"task_id"`
	SessionID string    `json:"session_id"`
	Agent     string    `json:"agent"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	dir string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) New(name, rootAgent string) (*Session, error) {
	now := time.Now()
	id := uuid.NewString()
	if name == "" {
		name = shortSessionID(id)
	}
	sess := &Session{
		ID:            id,
		Name:          name,
		RootAgent:     rootAgent,
		CreatedAt:     now,
		UpdatedAt:     now,
		VisibleSkills: map[string]bool{},
	}
	return sess, s.Save(sess)
}

func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (s *Store) Save(sess *Session) error {
	sess.UpdatedAt = time.Now()
	dir := s.sessionDir(sess.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "session.json"), data, 0o644)
}

func (s *Store) Delete(sess *Session) error {
	if sess == nil || sess.ID == "" {
		return nil
	}
	return os.RemoveAll(s.sessionDir(sess.ID))
}

func (s *Store) LoadByID(id string) (*Session, error) {
	return s.loadByID(id)
}

func HasContent(sess *Session) bool {
	if sess == nil {
		return false
	}
	if len(sess.SubAgents) > 0 {
		return true
	}
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "user", "tool":
			if strings.TrimSpace(msg.Content) != "" {
				return true
			}
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" || len(msg.ToolCalls) > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Store) Load(idOrName string) (*Session, error) {
	items, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == idOrName || item.Name == idOrName {
			return s.loadByID(item.ID)
		}
	}
	return nil, errors.New("session not found")
}

func (s *Store) Fork(src *Session, name string) (*Session, error) {
	cp := *src
	cp.ID = uuid.NewString()
	cp.Name = name
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = cp.CreatedAt
	cp.Messages = append([]types.ChatMessage(nil), src.Messages...)
	cp.SubAgents = append([]SubAgentRef(nil), src.SubAgents...)
	cp.VisibleSkills = map[string]bool{}
	for k, v := range src.VisibleSkills {
		cp.VisibleSkills[k] = v
	}
	return &cp, s.Save(&cp)
}

func (s *Store) UpsertSubAgent(sess *Session, ref SubAgentRef) error {
	if sess == nil || ref.TaskID == "" {
		return nil
	}
	now := time.Now()
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}
	if ref.UpdatedAt.IsZero() {
		ref.UpdatedAt = now
	}
	for i := range sess.SubAgents {
		if sess.SubAgents[i].TaskID != ref.TaskID {
			continue
		}
		if ref.SessionID != "" {
			sess.SubAgents[i].SessionID = ref.SessionID
		}
		if ref.Name != "" {
			sess.SubAgents[i].Name = ref.Name
		}
		if ref.Status != "" {
			sess.SubAgents[i].Status = ref.Status
		}
		if !ref.CreatedAt.IsZero() {
			sess.SubAgents[i].CreatedAt = ref.CreatedAt
		}
		sess.SubAgents[i].UpdatedAt = ref.UpdatedAt
		return s.Save(sess)
	}
	sess.SubAgents = append(sess.SubAgents, ref)
	return s.Save(sess)
}

func (s *Store) UpdateSubAgent(sess *Session, taskID, sessionID, status string) error {
	if sess == nil || taskID == "" {
		return nil
	}
	for i := range sess.SubAgents {
		if sess.SubAgents[i].TaskID != taskID {
			continue
		}
		if sessionID != "" {
			sess.SubAgents[i].SessionID = sessionID
		}
		if status != "" {
			sess.SubAgents[i].Status = status
		}
		sess.SubAgents[i].UpdatedAt = time.Now()
		return s.Save(sess)
	}
	return nil
}

func (s *Store) Rename(sess *Session, name string) error {
	sess.Name = name
	return s.Save(sess)
}

func (s *Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Session{}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		sess, err := s.loadByID(ent.Name())
		if err == nil {
			out = append(out, *sess)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Store) loadByID(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(s.sessionDir(id), "session.json"))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.dir, id)
}
