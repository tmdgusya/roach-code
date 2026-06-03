package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/config"
)

// TestSandboxStatusLine verifies that chatTUI.sandboxStatusLine renders the
// session's bash confinement as one line per state, reading m.bashSandbox.
// Assertions strip ANSI colour escapes so they hold regardless of NO_COLOR /
// theme, asserting on the literal text tokens from chat_approval.go.
func TestSandboxStatusLine(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		network  bool
		contains []string
		// excludes guards the enforce net-off / net-on distinction (both share
		// the "net o..." prefix, so the wrong marker must be absent).
		excludes []string
	}{
		{
			name:     "enforce net off",
			state:    "enforce",
			network:  false,
			contains: []string{"sandbox: enforce", "writes confined", "net off"},
			excludes: []string{"net on"},
		},
		{
			name:     "enforce net on",
			state:    "enforce",
			network:  true,
			contains: []string{"sandbox: enforce", "writes confined", "net on"},
			excludes: []string{"net off"},
		},
		{
			name:     "unavailable on this OS",
			state:    "unavailable",
			network:  false,
			contains: []string{"unavailable on this OS", "runs unconfined"},
		},
		{
			name:     "off renders UNCONFINED",
			state:    "off",
			network:  false,
			contains: []string{"UNCONFINED", "full disk + network access"},
		},
		{
			name:     "unknown empty state defaults to UNCONFINED",
			state:    "",
			network:  false,
			contains: []string{"UNCONFINED", "full disk + network access"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := chatTUI{bashSandbox: bashSandboxStatus{state: tc.state, network: tc.network}}
			got := ansi.Strip(m.sandboxStatusLine())
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("sandboxStatusLine()=%q, want substring %q", got, want)
				}
			}
			for _, bad := range tc.excludes {
				if strings.Contains(got, bad) {
					t.Errorf("sandboxStatusLine()=%q, must NOT contain %q", got, bad)
				}
			}
		})
	}
}

// TestBashSandboxFromConfig covers the platform-independent branches: network
// propagation from cfg.Sandbox.Network, and BashMode()!="enforce" -> "off".
// The enforce/unavailable split depends on sandbox.Available() (host-specific),
// so it is intentionally not asserted here.
func TestBashSandboxFromConfig(t *testing.T) {
	t.Run("bash off yields off state regardless of platform", func(t *testing.T) {
		// Sandbox.Bash == "off" forces BashMode() to "off", so the derived
		// state is "off" on every OS.
		cfg := &config.Config{}
		cfg.Sandbox.Bash = "off"
		cfg.Sandbox.Network = false

		st := bashSandboxFromConfig(cfg)
		if st.state != "off" {
			t.Errorf("state = %q, want %q", st.state, "off")
		}
		if st.network != false {
			t.Errorf("network = %v, want false", st.network)
		}
	})

	t.Run("network true propagates to status", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Sandbox.Bash = "off" // keep on the platform-independent branch
		cfg.Sandbox.Network = true

		st := bashSandboxFromConfig(cfg)
		if st.network != true {
			t.Errorf("network = %v, want true", st.network)
		}
		if st.state != "off" {
			t.Errorf("state = %q, want %q", st.state, "off")
		}
	})
}

// TestApprovalDestructive confirms the read-vs-rest category logic: only tools
// categorised "read" in toolCategory are benign; writers, exec, and unknown
// (MCP-style) names are destructive and default the gate to Deny.
func TestApprovalDestructive(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"read_file", false},            // category "read"
		{"bash", true},                  // category "exec"
		{"write_file", true},            // category "write"
		{"mcp__server__do_thing", true}, // unknown -> category "" -> destructive
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			if got := approvalDestructive(tc.tool); got != tc.want {
				t.Errorf("approvalDestructive(%q) = %v, want %v", tc.tool, got, tc.want)
			}
		})
	}
}
