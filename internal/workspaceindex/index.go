package workspaceindex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/session"
)

const filename = "workspaces.json"

var fileMu sync.Mutex

type Entry struct {
	Path          string    `json:"path"`
	LastSessionID string    `json:"last_session_id,omitempty"`
	LastOpenedAt  time.Time `json:"last_opened_at"`
}

type Index struct {
	Version    int     `json:"version"`
	Workspaces []Entry `json:"workspaces"`
}

type Workspace struct {
	Path          string            `json:"path"`
	Name          string            `json:"name"`
	LastSessionID string            `json:"last_session_id,omitempty"`
	LastOpenedAt  time.Time         `json:"last_opened_at"`
	Available     bool              `json:"available"`
	Sessions      []session.Session `json:"sessions"`
}

func Register(homeDir, workspace, sessionID string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	path, err := canonicalPath(workspace)
	if err != nil {
		return err
	}
	idx, err := read(homeDir)
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range idx.Workspaces {
		if samePath(idx.Workspaces[i].Path, path) {
			idx.Workspaces[i].Path = path
			idx.Workspaces[i].LastOpenedAt = now
			if strings.TrimSpace(sessionID) != "" {
				idx.Workspaces[i].LastSessionID = sessionID
			}
			return write(homeDir, idx)
		}
	}
	idx.Workspaces = append(idx.Workspaces, Entry{Path: path, LastSessionID: sessionID, LastOpenedAt: now})
	return write(homeDir, idx)
}

func MostRecent(homeDir string) (Entry, bool, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	idx, err := read(homeDir)
	if err != nil {
		return Entry{}, false, err
	}
	sort.Slice(idx.Workspaces, func(i, j int) bool { return idx.Workspaces[i].LastOpenedAt.After(idx.Workspaces[j].LastOpenedAt) })
	for _, entry := range idx.Workspaces {
		if info, statErr := os.Stat(entry.Path); statErr == nil && info.IsDir() {
			return entry, true, nil
		}
	}
	return Entry{}, false, nil
}

func Find(homeDir, workspace string) (Entry, bool, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	path, err := canonicalPath(workspace)
	if err != nil {
		return Entry{}, false, err
	}
	idx, err := read(homeDir)
	if err != nil {
		return Entry{}, false, err
	}
	for _, entry := range idx.Workspaces {
		if samePath(entry.Path, path) {
			return entry, true, nil
		}
	}
	return Entry{}, false, nil
}

func List(homeDir string) ([]Workspace, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	idx, err := read(homeDir)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(idx.Workspaces))
	for _, entry := range idx.Workspaces {
		item := Workspace{
			Path: entry.Path, Name: filepath.Base(entry.Path), LastSessionID: entry.LastSessionID,
			LastOpenedAt: entry.LastOpenedAt, Sessions: []session.Session{},
		}
		if info, statErr := os.Stat(entry.Path); statErr == nil && info.IsDir() {
			item.Available = true
			store := session.NewStore(filepath.Join(entry.Path, ".Asayn", ".sessions", "root_agents"))
			if sessions, listErr := store.List(); listErr == nil {
				item.Sessions = sessions
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastOpenedAt.After(out[j].LastOpenedAt) })
	return out, nil
}

func read(homeDir string) (Index, error) {
	data, err := os.ReadFile(filepath.Join(homeDir, filename))
	if errors.Is(err, os.ErrNotExist) {
		return Index{Version: 1, Workspaces: []Entry{}}, nil
	}
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, err
	}
	if idx.Version == 0 {
		idx.Version = 1
	}
	if idx.Workspaces == nil {
		idx.Workspaces = []Entry{}
	}
	return idx, nil
}

func write(homeDir string, idx Index) error {
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return err
	}
	idx.Version = 1
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(homeDir, filename), data, 0o644)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
