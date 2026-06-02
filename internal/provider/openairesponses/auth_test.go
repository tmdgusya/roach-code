package openairesponses

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"roach-code/internal/provider"
)

func TestPKCEPair(t *testing.T) {
	v, c := pkcePair()
	if v == "" || c == "" {
		t.Fatal("empty pkce")
	}
	if _, err := base64.RawURLEncoding.DecodeString(v); err != nil {
		t.Errorf("verifier not base64url: %v", err)
	}
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if c != want {
		t.Errorf("challenge = %q, want S256(verifier) = %q", c, want)
	}
	// Two calls must differ (random verifier).
	v2, _ := pkcePair()
	if v == v2 {
		t.Error("pkce verifier not random across calls")
	}
}

// fakeJWT builds an unsigned-but-well-formed JWT with the given payload object.
func fakeJWT(t *testing.T, payload any) string {
	t.Helper()
	b, _ := json.Marshal(payload)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(b) + ".sig"
}

func TestAccountIDFromIDToken(t *testing.T) {
	jwt := fakeJWT(t, map[string]any{
		"email": "u@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_123",
			"chatgpt_plan_type":  "pro",
		},
	})
	if got := accountIDFromIDToken(jwt); got != "acct_123" {
		t.Errorf("account id = %q, want acct_123", got)
	}
	if got := accountIDFromIDToken("not-a-jwt"); got != "" {
		t.Errorf("malformed jwt: got %q, want empty", got)
	}
}

func TestJWTExpiryAndNeedsRefresh(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	jwt := fakeJWT(t, map[string]any{"exp": future})
	exp, ok := jwtExpiry(jwt)
	if !ok || exp.Unix() != future {
		t.Fatalf("jwtExpiry = (%v,%v), want %d", exp, ok, future)
	}

	// Valid, recently refreshed → no refresh.
	a := &authData{Tokens: &tokenData{AccessToken: jwt}, LastRefresh: time.Now().UTC().Format(time.RFC3339)}
	if needsRefresh(a) {
		t.Error("fresh token should not need refresh")
	}
	// Near-expiry token → refresh.
	soon := fakeJWT(t, map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()})
	a2 := &authData{Tokens: &tokenData{AccessToken: soon}, LastRefresh: time.Now().UTC().Format(time.RFC3339)}
	if !needsRefresh(a2) {
		t.Error("near-expiry token should need refresh")
	}
	// Old last_refresh → refresh even if token looks valid.
	a3 := &authData{Tokens: &tokenData{AccessToken: jwt}, LastRefresh: time.Now().Add(-9 * 24 * time.Hour).UTC().Format(time.RFC3339)}
	if !needsRefresh(a3) {
		t.Error("stale last_refresh should need refresh")
	}
}

func TestNewSessionIDFormat(t *testing.T) {
	id := newSessionID()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Errorf("session id = %q, want uuid-shaped", id)
	}
	if id == newSessionID() {
		t.Error("session id not unique")
	}
}

// TestNoAPIKeyUsesOAuthBase verifies the mode/base-URL resolution: an entry with
// no API key routes to the ChatGPT backend (OAuth mode).
func TestNoAPIKeyUsesOAuthBase(t *testing.T) {
	p, err := New(provider.Config{Name: "codex", Model: "gpt-5-codex"}) // no APIKey
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*client)
	if c.baseURL != chatgptBaseURL {
		t.Errorf("base URL = %q, want %q (OAuth mode)", c.baseURL, chatgptBaseURL)
	}
	if _, ok := c.cred.(*oauthCred); !ok {
		t.Errorf("cred = %T, want *oauthCred", c.cred)
	}
}

func TestAPIKeyUsesConfiguredBase(t *testing.T) {
	p, err := New(provider.Config{Name: "codex", Model: "gpt-5-codex", BaseURL: "https://api.openai.com/v1", APIKey: "sk-x"})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*client)
	if c.baseURL != "https://api.openai.com/v1" {
		t.Errorf("base URL = %q, want configured", c.baseURL)
	}
	if _, ok := c.cred.(apiKeyCred); !ok {
		t.Errorf("cred = %T, want apiKeyCred", c.cred)
	}
}

// TestExplicitAuthModeWins checks that Config.Extra["auth"] overrides the
// key-presence auto-detection.
func TestExplicitAuthModeWins(t *testing.T) {
	// auth=oauth with a key present → still OAuth (chatgpt backend).
	p, err := New(provider.Config{Name: "codex", Model: "gpt-5.5", BaseURL: "https://api.openai.com/v1", APIKey: "sk-x", Extra: map[string]any{"auth": "oauth"}})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*client)
	if _, ok := c.cred.(*oauthCred); !ok || c.baseURL != chatgptBaseURL {
		t.Errorf("auth=oauth: cred=%T base=%q, want oauthCred + %q", c.cred, c.baseURL, chatgptBaseURL)
	}

	// auth=apikey with NO key → still api-key mode (configured base), defers the
	// missing-key error to request time.
	p2, err := New(provider.Config{Name: "codex", Model: "gpt-5.5", BaseURL: "https://api.openai.com/v1", Extra: map[string]any{"auth": "apikey"}})
	if err != nil {
		t.Fatal(err)
	}
	c2 := p2.(*client)
	if _, ok := c2.cred.(apiKeyCred); !ok || c2.baseURL != "https://api.openai.com/v1" {
		t.Errorf("auth=apikey: cred=%T base=%q, want apiKeyCred + configured base", c2.cred, c2.baseURL)
	}
}
