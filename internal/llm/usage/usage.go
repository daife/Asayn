package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

type Record struct {
	Timestamp        time.Time `json:"timestamp"`
	SessionID        string    `json:"session_id"`
	SessionName      string    `json:"session_name"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CacheHitTokens   int       `json:"cache_hit_tokens"`
	CacheMissTokens  int       `json:"cache_miss_tokens"`
}

type Tracker struct {
	paths config.Paths
	mu    sync.RWMutex
}

func NewTracker(paths config.Paths) *Tracker {
	return &Tracker{paths: paths}
}

func (t *Tracker) Log(sessionID, sessionName, model string, usage types.Usage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	record := Record{
		Timestamp:        time.Now(),
		SessionID:        sessionID,
		SessionName:      sessionName,
		Model:            model,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CacheHitTokens:   usage.PromptCacheHitTokens,
		CacheMissTokens:  usage.PromptCacheMissTokens,
	}

	path := t.paths.HomePath("usage.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

type Stats struct {
	TotalInput      int64
	TotalOutput     int64
	TotalCacheHit   int64
	SessionInput    int64
	SessionOutput   int64
	SessionCacheHit int64
}

func (t *Tracker) GetStats(sessionID string) (Stats, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	path := t.paths.HomePath("usage.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Stats{}, nil
	}
	if err != nil {
		return Stats{}, err
	}
	defer f.Close()

	var stats Stats
	dec := json.NewDecoder(f)
	for dec.More() {
		var r Record
		if err := dec.Decode(&r); err != nil {
			continue
		}
		stats.TotalInput += int64(r.PromptTokens)
		stats.TotalOutput += int64(r.CompletionTokens)
		stats.TotalCacheHit += int64(r.CacheHitTokens)

		if r.SessionID == sessionID {
			stats.SessionInput += int64(r.PromptTokens)
			stats.SessionOutput += int64(r.CompletionTokens)
			stats.SessionCacheHit += int64(r.CacheHitTokens)
		}
	}
	return stats, nil
}

func FormatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	f := float64(n)
	if n < 1000000 {
		return fmt.Sprintf("%.1fK", f/1000)
	}
	if n < 1000000000 {
		return fmt.Sprintf("%.1fM", f/1000000)
	}
	return fmt.Sprintf("%.1fB", f/1000000000)
}
