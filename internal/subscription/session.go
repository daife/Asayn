// Package subscription implements local Sign in with ChatGPT sessions.
package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const Resource = "https://api.openai.com/v1"
const issuer = "https://auth.openai.com"
const directScope = "chatgpt.tokens.use.direct"

type Session struct {
	ClientID          string    `json:"client_id"`
	Subject           string    `json:"subject"`
	Email             string    `json:"email"`
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	IDToken           string    `json:"id_token"`
	Scope             string    `json:"scope"`
	ExpiresAt         time.Time `json:"expires_at"`
	EarliestRefreshAt int64     `json:"earliest_refresh_at"`
}

type tokenResponse struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	IDToken           string `json:"id_token"`
	Scope             string `json:"scope"`
	ExpiresIn         int    `json:"expires_in"`
	EarliestRefreshAt int64  `json:"earliest_refresh_at"`
	Error             string `json:"error"`
}

func DefaultPath(account string) (string, error) {
	if account == "" {
		account = "default"
	}
	for _, r := range account {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", fmt.Errorf("invalid subscription account label")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".Asayn", "subscription", account+".json"), nil
}

func lock(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	l := flock.New(path + ".lock")
	ok, err := l.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ctx.Err()
	}
	return func() { _ = l.Unlock(); _ = l.Close() }, nil
}

func load(path string) (Session, error) {
	var s Session
	b, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("subscription session unavailable; run asayn subscription login <account>: %w", err)
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

func save(path string, s Session) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func hasDirect(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == directScope {
			return true
		}
	}
	return false
}

func exchange(ctx context.Context, client *http.Client, endpoint string, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return t, fmt.Errorf("OAuth token request failed")
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return t, fmt.Errorf("invalid OAuth token response (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 || t.Error != "" {
		return t, fmt.Errorf("OAuth token request HTTP %d: %s", resp.StatusCode, t.Error)
	}
	if t.AccessToken == "" || t.RefreshToken == "" || t.ExpiresIn <= 0 {
		return t, fmt.Errorf("incomplete OAuth token response")
	}
	return t, nil
}

func apply(s *Session, t tokenResponse) {
	s.AccessToken, s.RefreshToken = t.AccessToken, t.RefreshToken
	if t.IDToken != "" {
		s.IDToken = t.IDToken
	}
	if t.Scope != "" {
		s.Scope = t.Scope
	}
	s.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	s.EarliestRefreshAt = t.EarliestRefreshAt
}

func AccessToken(ctx context.Context, path string) (string, error) {
	return accessToken(ctx, path, &http.Client{Timeout: 30 * time.Second}, issuer+"/api/accounts/oauth/token")
}

func accessToken(ctx context.Context, path string, client *http.Client, endpoint string) (string, error) {
	unlock, err := lock(ctx, path)
	if err != nil {
		return "", err
	}
	defer unlock()
	s, err := load(path)
	if err != nil {
		return "", err
	}
	if s.ClientID == "" || s.ClientID == "dynamic_agent_client" || !hasDirect(s.Scope) {
		return "", fmt.Errorf("ChatGPT plan permission missing; sign in again")
	}
	if time.Until(s.ExpiresAt) < time.Minute && time.Now().Unix() >= s.EarliestRefreshAt {
		if s.RefreshToken == "" {
			return "", fmt.Errorf("subscription session signed out; sign in again")
		}
		t, refreshErr := exchange(ctx, client, endpoint, url.Values{"grant_type": {"refresh_token"}, "client_id": {s.ClientID}, "refresh_token": {s.RefreshToken}, "resource": {Resource}})
		if refreshErr != nil {
			switch t.Error {
			case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
				s.AccessToken, s.RefreshToken, s.IDToken = "", "", ""
				if err := save(path, s); err != nil {
					return "", err
				}
			}
			return "", refreshErr
		}
		apply(&s, t)
		if err = save(path, s); err != nil {
			return "", err
		}
	}
	if !hasDirect(s.Scope) || s.AccessToken == "" || !time.Now().Before(s.ExpiresAt) {
		return "", fmt.Errorf("ChatGPT plan session unavailable; sign in again")
	}
	return s.AccessToken, nil
}

// Models preserves the account catalog's display order and exposes only listed models.
func Models(ctx context.Context, path string) ([]Model, error) {
	token, err := AccessToken(ctx, path)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", Resource+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("model catalog HTTP %d (request ID %s)", resp.StatusCode, resp.Header.Get("x-request-id"))
	}
	var result struct {
		Models []Model `json:"models"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return nil, err
	}
	models := []Model{}
	for _, m := range result.Models {
		if m.Visibility == "list" {
			models = append(models, m)
		}
	}
	return models, nil
}

type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
}
