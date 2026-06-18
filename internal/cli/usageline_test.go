package cli

import (
	"strings"
	"testing"

	"roach-code/internal/provider"
)

// TestUsageLineTPS verifies that the TPS suffix is appended when tps > 0
// and omitted when tps == 0.
func TestUsageLineTPS(t *testing.T) {
	// Save/restore color state so we exercise the themed (colored) code path.
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = true

	u := &provider.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		CacheHitTokens:   800,
		CacheMissTokens:  200,
	}
	p := &provider.Pricing{
		CacheHit: 0.15,
		Input:    3.0,
		Output:   15.0,
	}

	t.Run("tps zero omitted", func(t *testing.T) {
		got := usageLine(u, p, 0)
		if strings.Contains(got, "@") {
			t.Fatalf("expected no TPS suffix when tps=0, got:\n%s", got)
		}
	})

	t.Run("tps positive appended", func(t *testing.T) {
		got := usageLine(u, p, 3500)
		if !strings.Contains(got, "@ 3.5K/s") {
			t.Fatalf("expected TPS suffix \"@ 3.5K/s\", got:\n%s", got)
		}
	})

	t.Run("tps different value", func(t *testing.T) {
		got := usageLine(u, p, 42)
		if !strings.Contains(got, "@ 42/s") {
			t.Fatalf("expected TPS suffix \"@ 42/s\", got:\n%s", got)
		}
	})

	t.Run("nil usage returns empty", func(t *testing.T) {
		if got := usageLine(nil, nil, 3500); got != "" {
			t.Fatalf("expected empty for nil usage, got: %q", got)
		}
	})
}

// TestUsageLineTPSNoColor verifies usageLine works without color (uses
// agent.FormatUsageLine as fallback, which does not include TPS).
func TestUsageLineTPSNoColor(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = false

	u := &provider.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	p := &provider.Pricing{
		Input:  3.0,
		Output: 15.0,
	}

	got := usageLine(u, p, 3500)
	// No-color path delegates to agent.FormatUsageLine; TPS is not added there.
	if got == "" {
		t.Fatal("expected non-empty no-color usage line")
	}
	if strings.Contains(got, "@") {
		t.Fatalf("no-color path should NOT include TPS, got:\n%s", got)
	}
}
