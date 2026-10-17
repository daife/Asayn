package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshRotationSerialized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account.json")
	if err := save(path, Session{ClientID: "issued", Scope: directScope, RefreshToken: "old", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old" || r.Form.Get("client_id") != "issued" || r.Form.Get("resource") != Resource || r.Form.Has("scope") {
			t.Error("incorrect refresh grant")
		}
		fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"replacement","expires_in":3600}`)
	}))
	defer server.Close()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := accessToken(context.Background(), path, server.Client(), server.URL)
			if err != nil || token != "new-access" {
				t.Errorf("token=%q err=%v", token, err)
			}
		}()
	}
	wg.Wait()
	s, err := load(path)
	if err != nil || s.RefreshToken != "replacement" || calls.Load() != 1 {
		t.Fatalf("refresh rotation failed: %v calls=%d", err, calls.Load())
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credentials are not private")
	}
}

func TestRefreshFailurePreservesOrClearsCredentials(t *testing.T) {
	for _, code := range []string{"invalid_grant", "server_error"} {
		t.Run(code, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "account.json")
			if err := save(path, Session{ClientID: "issued", Scope: directScope, RefreshToken: "old", AccessToken: "expired", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); fmt.Fprintf(w, `{"error":%q}`, code) }))
			defer server.Close()
			if _, err := accessToken(context.Background(), path, server.Client(), server.URL); err == nil {
				t.Fatal("expected error")
			}
			s, _ := load(path)
			if s.ClientID != "issued" || (s.RefreshToken == "") != (code == "invalid_grant") {
				t.Fatal("incorrect credential recovery")
			}
		})
	}
}

func TestAccountLabels(t *testing.T) {
	for _, label := range []string{"../secret", "a/b", "a\\b", "."} {
		if _, err := DefaultPath(label); err == nil {
			t.Errorf("accepted %q", label)
		}
	}
}
