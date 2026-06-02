package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRewriteProviderModelsPlural(t *testing.T) {
	src := `default_model = "glm-5.1"

[[providers]]
name        = "glm-5.1"
kind        = "openai"
base_url    = "https://api.z.ai/api/coding/paas/v4"
models      = ["glm-5-turbo", "glm-5.1"]
default     = "glm-5.1"
api_key_env = "ZAI_API_KEY"
thinking    = "enabled"

[[providers]]
name        = "minimax-m3"
kind        = "openai"
models      = ["MiniMax-M3"]
default     = "MiniMax-M3"
`
	out, missing := RewriteProviderModels(src, map[string]ModelsUpdate{
		"glm-5.1": {Models: []string{"glm-4.6", "glm-5", "glm-5.1"}, Default: "glm-5.1"},
	})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if !strings.Contains(out, `models      = ["glm-4.6", "glm-5", "glm-5.1"]`) {
		t.Errorf("glm models not rewritten:\n%s", out)
	}
	// Untouched provider preserved exactly.
	if !strings.Contains(out, `models      = ["MiniMax-M3"]`) {
		t.Errorf("minimax block should be untouched:\n%s", out)
	}
	// Surrounding fields preserved.
	if !strings.Contains(out, `thinking    = "enabled"`) {
		t.Errorf("thinking line lost:\n%s", out)
	}
	// Must still parse.
	var c Config
	if _, err := toml.Decode(out, &c); err != nil {
		t.Fatalf("rewritten TOML does not parse: %v\n%s", err, out)
	}
	g, _ := c.Provider("glm-5.1")
	if g == nil || len(g.Models) != 3 || g.Default != "glm-5.1" {
		t.Errorf("decoded glm wrong: %+v", g)
	}
}

func TestRewriteProviderModelsPreservesAuthAndComments(t *testing.T) {
	src := `[[providers]]
# Codex via ChatGPT subscription
name        = "codex"
kind        = "openai-responses"
models      = ["gpt-5.5"]
default     = "gpt-5.5"
auth        = "oauth"
effort      = "medium"
`
	out, _ := RewriteProviderModels(src, map[string]ModelsUpdate{
		"codex": {Models: []string{"gpt-5.5", "gpt-6"}, Default: "gpt-5.5"},
	})
	if !strings.Contains(out, `auth        = "oauth"`) {
		t.Errorf("auth field dropped:\n%s", out)
	}
	if !strings.Contains(out, "# Codex via ChatGPT subscription") {
		t.Errorf("comment dropped:\n%s", out)
	}
	if !strings.Contains(out, `models      = ["gpt-5.5", "gpt-6"]`) {
		t.Errorf("models not rewritten:\n%s", out)
	}
}

func TestRewriteProviderModelsSingularUpgrade(t *testing.T) {
	src := `[[providers]]
name        = "mimo-pro"
kind        = "openai"
model       = "mimo-v2.5-pro"
api_key_env = "MIMO_API_KEY"
`
	out, _ := RewriteProviderModels(src, map[string]ModelsUpdate{
		"mimo-pro": {Models: []string{"mimo-v2.5-pro", "mimo-v3"}, Default: "mimo-v2.5-pro"},
	})
	if strings.Contains(out, `model       = "mimo-v2.5-pro"`) {
		t.Errorf("singular model line should be replaced:\n%s", out)
	}
	if !strings.Contains(out, `models      = ["mimo-v2.5-pro", "mimo-v3"]`) {
		t.Errorf("models list not written:\n%s", out)
	}
	if !strings.Contains(out, `default     = "mimo-v2.5-pro"`) {
		t.Errorf("default not inserted:\n%s", out)
	}
	var c Config
	if _, err := toml.Decode(out, &c); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, out)
	}
}

func TestRewriteProviderModelsMissing(t *testing.T) {
	src := "[[providers]]\nname        = \"glm-5.1\"\nmodels      = [\"glm-5.1\"]\n"
	_, missing := RewriteProviderModels(src, map[string]ModelsUpdate{
		"glm-5.1": {Models: []string{"glm-5.1"}, Default: "glm-5.1"},
		"nope":    {Models: []string{"x"}},
	})
	if len(missing) != 1 || missing[0] != "nope" {
		t.Errorf("missing = %v, want [nope]", missing)
	}
}

func TestRewriteProviderModelsPreservesCRLF(t *testing.T) {
	src := "[[providers]]\r\nname        = \"glm-5.1\"\r\nmodels      = [\"glm-5.1\"]\r\ndefault     = \"glm-5.1\"\r\n"
	out, _ := RewriteProviderModels(src, map[string]ModelsUpdate{
		"glm-5.1": {Models: []string{"glm-5.1", "glm-6"}, Default: "glm-5.1"},
	})
	if !strings.Contains(out, "\r\n") {
		t.Errorf("CRLF line endings not preserved:\n%q", out)
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", "\n"), "\r") {
		t.Errorf("stray CR left behind:\n%q", out)
	}
}
