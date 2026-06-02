package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/asayn/asayn/internal/llm/types"
	"github.com/google/uuid"
)

type Session struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	RootAgent     string              `json:"root_agent"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Messages      []types.ChatMessage `json:"messages"`
	VisibleSkills map[string]bool     `json:"visible_skills"`
	Changes       []FileChange        `json:"changes"`
}

type FileChange struct {
	ID            string    `json:"id"`
	At            time.Time `json:"at"`
	Path          string    `json:"path"`
	Action        string    `json:"action"`
	BeforeContent string    `json:"before_content"`
	AfterContent  string    `json:"after_content"`
	UnifiedDiff   string    `json:"unified_diff"`
}

type Store struct {
	dir string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) New(name, rootAgent string) (*Session, error) {
	if name == "" {
		name = "session"
	}
	now := time.Now()
	sess := &Session{
		ID:            uuid.NewString(),
		Name:          name,
		RootAgent:     rootAgent,
		CreatedAt:     now,
		UpdatedAt:     now,
		VisibleSkills: map[string]bool{},
	}
	return sess, s.Save(sess)
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
	cp.Changes = append([]FileChange(nil), src.Changes...)
	cp.VisibleSkills = map[string]bool{}
	for k, v := range src.VisibleSkills {
		cp.VisibleSkills[k] = v
	}
	return &cp, s.Save(&cp)
}

func (s *Store) Rename(sess *Session, name string) error {
	sess.Name = name
	return s.Save(sess)
}

func (s *Store) AddChange(sess *Session, change FileChange) error {
	if change.ID == "" {
		change.ID = uuid.NewString()
	}
	if change.At.IsZero() {
		change.At = time.Now()
	}
	sess.Changes = append(sess.Changes, change)
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
