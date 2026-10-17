package subscription

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
)

func randomValue() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Login keeps the selected account unchanged until identity and nonce are verified.
func Login(ctx context.Context, path string, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	unlock, err := lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	hostPath := filepath.Join(filepath.Dir(path), "host-id")
	hostUnlock, err := lock(ctx, hostPath)
	if err != nil {
		return err
	}
	host, err := os.ReadFile(hostPath)
	if os.IsNotExist(err) {
		host = []byte("urn:uuid:" + uuid.NewString())
		err = os.WriteFile(hostPath, host, 0600)
	}
	hostUnlock()
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("OpenAI identity discovery failed")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/auth/callback"
	state, nonce, verifier := randomValue(), randomValue(), randomValue()
	challenge := sha256.Sum256([]byte(verifier))
	clientID := s.ClientID
	if clientID == "" {
		clientID = "dynamic_agent_client"
	}
	q := url.Values{"client_id": {clientID}, "ext_agent_host_id": {string(host)}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {"openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"}, "resource": {Resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if clientID == "dynamic_agent_client" {
		q.Set("agent_name_hint", "Asayn")
	} else if s.IDToken != "" {
		q.Set("id_token_hint", s.IDToken)
	}
	type callback struct {
		code, clientID string
		err            error
	}
	result := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		values := r.URL.Query()
		if r.Method != "GET" || subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state", 400)
			return
		}
		c := callback{code: values.Get("code"), clientID: values.Get("client_id")}
		if values.Get("error") != "" {
			c.err = fmt.Errorf("ChatGPT authorization declined or failed")
		}
		if c.code == "" && c.err == nil {
			c.err = fmt.Errorf("authorization callback missing code")
		}
		select {
		case result <- c:
			fmt.Fprint(w, "Return to Asayn to finish signing in.")
		default:
			http.Error(w, "Authorization already received", 409)
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	authURL := provider.Endpoint().AuthURL + "?" + q.Encode()
	fmt.Fprintln(output, "Continue with ChatGPT: opening your browser…")
	if err = openBrowser(authURL); err != nil {
		// Never print returning authorization URLs containing an ID token hint.
		if q.Get("id_token_hint") != "" {
			return fmt.Errorf("could not open browser for returning sign-in")
		}
		fmt.Fprintln(output, authURL)
	}
	var cb callback
	select {
	case cb = <-result:
	case <-ctx.Done():
		return ctx.Err()
	}
	if cb.err != nil {
		return cb.err
	}
	if clientID == "dynamic_agent_client" {
		if cb.clientID == "" || cb.clientID == "dynamic_agent_client" {
			return fmt.Errorf("registration did not issue a client ID")
		}
		clientID = cb.clientID
	} else if cb.clientID != "" && cb.clientID != clientID {
		return fmt.Errorf("authorization returned a different client ID")
	}
	// Retain the issued registration even if exchange fails, so reauthorization reuses it.
	pending := s
	pending.ClientID = clientID
	if err = save(path, pending); err != nil {
		return err
	}
	t, err := exchange(ctx, client, provider.Endpoint().TokenURL, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {cb.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {Resource}})
	if err != nil {
		return err
	}
	id, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(ctx, t.IDToken)
	if err != nil {
		return fmt.Errorf("OpenAI ID token validation failed")
	}
	if subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(nonce)) != 1 || id.Subject == "" {
		return fmt.Errorf("invalid OpenAI identity nonce or subject")
	}
	if s.Subject != "" && s.Subject != id.Subject {
		return fmt.Errorf("signed-in identity does not match selected account; use a different account label")
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err = id.Claims(&claims); err != nil {
		return fmt.Errorf("invalid identity claims")
	}
	s.ClientID, s.Subject, s.Email = clientID, id.Subject, claims.Email
	apply(&s, t)
	s.Scope = t.Scope
	if err = save(path, s); err != nil {
		return err
	}
	fmt.Fprintf(output, "Signed in: %s (%s)\n", s.Email, s.ClientID)
	if !hasDirect(s.Scope) {
		return fmt.Errorf("signed in, but ChatGPT plan usage was not granted; inference is disabled")
	}
	return nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Run()
}

func Logout(ctx context.Context, path string) error {
	unlock, err := lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := load(path)
	if err != nil {
		return err
	}
	var revokeErr error
	if s.RefreshToken != "" {
		client := &http.Client{Timeout: 30 * time.Second}
		p, e := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
		var metadata struct {
			RevocationEndpoint string `json:"revocation_endpoint"`
		}
		if e == nil {
			e = p.Claims(&metadata)
		}
		if e == nil && metadata.RevocationEndpoint != "" {
			form := url.Values{"token": {s.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {s.ClientID}}
			for attempt := 0; attempt < 3; attempt++ {
				req, _ := http.NewRequestWithContext(ctx, "POST", metadata.RevocationEndpoint, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, requestErr := client.Do(req)
				if requestErr == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						revokeErr = nil
						break
					}
					revokeErr = fmt.Errorf("revocation HTTP %d", resp.StatusCode)
					if resp.StatusCode < 500 {
						break
					}
				} else {
					revokeErr = fmt.Errorf("revocation network failure")
				}
				select {
				case <-ctx.Done():
					attempt = 3
				case <-time.After(time.Duration(1<<attempt) * time.Second):
				}
			}
		} else {
			revokeErr = fmt.Errorf("revocation discovery failed")
		}
	}
	s.AccessToken, s.RefreshToken, s.IDToken = "", "", ""
	if err = save(path, s); err != nil {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("signed out locally; remote revocation unconfirmed: %v; disconnect Asayn in ChatGPT Settings", revokeErr)
	}
	return nil
}
