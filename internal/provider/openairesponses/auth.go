package openairesponses

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// --- Codex "Sign in with ChatGPT" OAuth (PKCE) ---
//
// Mirrors the official Codex CLI flow so a roach-code user can authenticate with
// a ChatGPT subscription instead of an API key. Constants verified against
// github.com/openai/codex (codex-rs). Header names are case-insensitive on the
// wire, so casing differences across clients don't matter.

const (
	oauthClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthAuthorize = "https://auth.openai.com/oauth/authorize"
	oauthToken     = "https://auth.openai.com/oauth/token"
	oauthScope     = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	// chatgptBaseURL is the Responses endpoint host for ChatGPT-subscription auth.
	chatgptBaseURL = "https://chatgpt.com/backend-api/codex"
	originator     = "codex_cli_rs"

	refreshWindow   = 5 * time.Minute
	refreshInterval = 8 * 24 * time.Hour
)

// loopback ports the callback server tries, in order (matches Codex).
var oauthPorts = []int{1455, 1457}

const oauthRedirectPath = "/auth/callback"

// authData is the on-disk credential file, shaped like Codex's auth.json.
type authData struct {
	OpenAIAPIKey string     `json:"OPENAI_API_KEY,omitempty"`
	Tokens       *tokenData `json:"tokens,omitempty"`
	LastRefresh  string     `json:"last_refresh,omitempty"`
	AuthMode     string     `json:"auth_mode,omitempty"`
}

type tokenData struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

// authPath returns the credential file location (roach-code config dir).
func authPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "roach-code", "codex-auth.json"), nil
}

func loadAuth() (*authData, error) {
	p, err := authPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var a authData
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &a, nil
}

func saveAuth(a *authData) error {
	p, err := authPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// hasCodexAuth reports whether a usable ChatGPT-OAuth credential is stored.
func hasCodexAuth() bool {
	a, err := loadAuth()
	return err == nil && a.Tokens != nil && a.Tokens.AccessToken != ""
}

// --- credential abstraction (so OAuth and API-key share one request path) ---

type credential interface {
	apply(ctx context.Context, req *http.Request) error
}

type apiKeyCred struct {
	key    string
	keyEnv string
}

func (c apiKeyCred) apply(_ context.Context, req *http.Request) error {
	if c.key == "" {
		hint := "OPENAI_API_KEY"
		if c.keyEnv != "" {
			hint = c.keyEnv
		}
		return fmt.Errorf("no API key (%s) and no ChatGPT login — set the key or run `roach-code codex login`", hint)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	return nil
}

type oauthCred struct {
	http      *http.Client
	sessionID string
	mu        sync.Mutex
}

func (o *oauthCred) apply(ctx context.Context, req *http.Request) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	a, err := loadAuth()
	if err != nil || a.Tokens == nil || a.Tokens.AccessToken == "" {
		return fmt.Errorf("not signed in to ChatGPT — run `roach-code codex login`")
	}
	if needsRefresh(a) {
		if err := refreshTokens(ctx, o.http, a); err != nil {
			return fmt.Errorf("token refresh failed (run `roach-code codex login` to re-auth): %w", err)
		}
	}
	req.Header.Set("Authorization", "Bearer "+a.Tokens.AccessToken)
	if a.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", a.Tokens.AccountID)
	}
	req.Header.Set("originator", originator)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("session-id", o.sessionID)
	return nil
}

// needsRefresh is true when the access token is within refreshWindow of expiry
// or the last refresh is older than refreshInterval.
func needsRefresh(a *authData) bool {
	if exp, ok := jwtExpiry(a.Tokens.AccessToken); ok {
		if time.Until(exp) <= refreshWindow {
			return true
		}
	}
	if a.LastRefresh != "" {
		if t, err := time.Parse(time.RFC3339, a.LastRefresh); err == nil {
			return time.Since(t) >= refreshInterval
		}
	}
	return false
}

func refreshTokens(ctx context.Context, hc *http.Client, a *authData) error {
	body, _ := json.Marshal(map[string]string{
		"client_id":     oauthClientID,
		"grant_type":    "refresh_token",
		"refresh_token": a.Tokens.RefreshToken,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthToken, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return err
	}
	if tok.AccessToken != "" {
		a.Tokens.AccessToken = tok.AccessToken
	}
	if tok.IDToken != "" {
		a.Tokens.IDToken = tok.IDToken
	}
	if tok.RefreshToken != "" {
		a.Tokens.RefreshToken = tok.RefreshToken
	}
	a.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	return saveAuth(a)
}

// jwtExpiry reads the exp claim (unix seconds) from a JWT without verifying it.
func jwtExpiry(jwt string) (time.Time, bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &c); err != nil || c.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(c.Exp, 0), true
}

// accountIDFromIDToken extracts the ChatGPT account id from the id_token's
// "https://api.openai.com/auth" claim.
func accountIDFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return ""
	}
	return c.Auth.ChatGPTAccountID
}

// --- PKCE ---

func pkcePair() (verifier, challenge string) {
	buf := make([]byte, 64)
	_, _ = rand.Read(buf)
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func randToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Login runs the interactive ChatGPT OAuth flow: it starts a loopback callback
// server, opens the browser to the authorize URL, exchanges the returned code
// for tokens, and writes them to the credential file. It blocks until the
// callback arrives or ctx is cancelled.
func Login(ctx context.Context) error {
	verifier, challenge := pkcePair()
	state := randToken(24)

	ln, port, err := listenLoopback()
	if err != nil {
		return fmt.Errorf("could not bind a loopback port (%v); is another login in progress?", err)
	}
	redirectURI := fmt.Sprintf("http://localhost:%d%s", port, oauthRedirectPath)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc(oauthRedirectPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			errCh <- fmt.Errorf("authorization denied: %s", e)
			fmt.Fprint(w, "<html><body>Login failed. You may close this window.</body></html>")
			return
		}
		if q.Get("state") != state {
			errCh <- fmt.Errorf("state mismatch (possible CSRF) — login aborted")
			fmt.Fprint(w, "<html><body>Login failed (state mismatch). You may close this window.</body></html>")
			return
		}
		fmt.Fprint(w, "<html><body><h2>roach-code: signed in to ChatGPT.</h2>You may close this window and return to the terminal.</body></html>")
		codeCh <- q.Get("code")
	})
	srv.Handler = mux
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {oauthClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {oauthScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {originator},
	}
	// url.Values.Encode() encodes spaces as "+". OpenAI's authorize endpoint does
	// not decode "+" to space in the scope param, so the scopes arrive malformed
	// ("openid+profile+…") and Hydra rejects the request (authorize_hydra_invalid_request).
	// Use %20 instead. Safe here: none of our values legitimately contain a literal "+"
	// (challenge/state are base64url with -/_, the rest have no "+").
	authURL := oauthAuthorize + "?" + strings.ReplaceAll(q.Encode(), "+", "%20")

	fmt.Println("Opening your browser to sign in with ChatGPT…")
	fmt.Println("If it doesn't open, visit:\n  " + authURL)
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case code := <-codeCh:
		return exchangeCode(ctx, code, verifier, redirectURI)
	}
}

func exchangeCode(ctx context.Context, code, verifier, redirectURI string) error {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {oauthClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthToken, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token exchange status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return err
	}
	a := &authData{
		AuthMode:    "chatgpt",
		LastRefresh: time.Now().UTC().Format(time.RFC3339),
		Tokens: &tokenData{
			IDToken:      tok.IDToken,
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			AccountID:    accountIDFromIDToken(tok.IDToken),
		},
	}
	if err := saveAuth(a); err != nil {
		return err
	}
	p, _ := authPath()
	fmt.Printf("Signed in. Credentials saved to %s\n", p)
	return nil
}

// Status returns a one-line human summary of the stored ChatGPT credential, or
// an error if not signed in.
func Status() (string, error) {
	a, err := loadAuth()
	if err != nil || a.Tokens == nil || a.Tokens.AccessToken == "" {
		return "", fmt.Errorf("not signed in — run `roach-code codex login`")
	}
	acct := a.Tokens.AccountID
	if acct == "" {
		acct = "(unknown account)"
	}
	exp := "unknown expiry"
	if t, ok := jwtExpiry(a.Tokens.AccessToken); ok {
		if time.Until(t) <= 0 {
			exp = "access token expired (will refresh on next use)"
		} else {
			exp = "access token valid for ~" + time.Until(t).Round(time.Minute).String()
		}
	}
	p, _ := authPath()
	return fmt.Sprintf("signed in to ChatGPT · account %s · %s · %s", acct, exp, p), nil
}

// Logout removes the stored ChatGPT credential.
func Logout() error {
	p, err := authPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func listenLoopback() (net.Listener, int, error) {
	var lastErr error
	for _, port := range oauthPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
