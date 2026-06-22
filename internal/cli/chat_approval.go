package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/config"
	"roach-code/internal/i18n"
	"roach-code/internal/sandbox"
	"roach-code/internal/tool"
)

// handleApprovalKey resolves a pending approval from a keystroke and re-arms the
// listener. ↑/↓ (or k/j) move the highlighted choice; Enter activates the
// highlighted row — which DEFAULTS to Deny for destructive calls, so a reflexive
// Enter denies rather than grants. 1/y still allows once, 2/a allows for the
// session, 3/n/Esc deny, and Ctrl-C cancels the whole turn.
func (m chatTUI) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	answer := func(allow, session bool) (tea.Model, tea.Cmd) {
		// Restore the thought force-expanded for this gate to the verbose setting now
		// the decision is made (re-collapse unless /verbose is on).
		if m.approvalExpandedThought >= 0 {
			if m.approvalExpandedThought < len(m.thoughts) {
				m.renderThought(m.thoughts[m.approvalExpandedThought], m.showReasoning, 0)
				m.transcriptDirty = true
			}
			m.approvalExpandedThought = -1
		}
		m.ctrl.Approve(m.pendingApproval.ID, allow, session)
		m.pendingApproval = nil
		m.pendingApprovalParent = ""
		return m, nil // the next ApprovalRequest / event arrives on eventCh
	}
	switch msg.String() {
	case "ctrl+c":
		m.ctrl.Cancel() // cancels the run; the approver unblocks via ctx.Done()
		return answer(false, false)
	case "up", "k":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
		return m, nil
	case "down", "j":
		if m.approvalCursor < 2 {
			m.approvalCursor++
		}
		return m, nil
	case "ctrl+o":
		// The global Ctrl+O is otherwise swallowed while a gate is up; honour it here
		// so the user can expand/collapse the rationale at the moment they most want
		// to read it, keeping the gated thought open regardless of the new setting.
		m.toggleVerboseReasoning(false)
		if m.approvalExpandedThought >= 0 && m.approvalExpandedThought < len(m.thoughts) {
			m.renderThought(m.thoughts[m.approvalExpandedThought], true, reasoningApprovalLines)
			m.transcriptDirty = true
		}
		return m, nil
	case "enter":
		switch {
		case m.approvalCursor == 0:
			return answer(true, false)
		case m.approvalCursor == 1:
			return answer(true, true)
		default:
			return answer(false, false)
		}
	case "esc":
		return answer(false, false)
	}
	switch strings.ToLower(msg.String()) {
	case "y", "1":
		return answer(true, false)
	case "a", "2":
		return answer(true, true)
	case "n", "3":
		return answer(false, false)
	}
	return m, nil // ignore anything else while awaiting a decision
}

// renderApprovalBanner is the decision card shown above the input while a tool
// call (or a plan) awaits the user. It shares the chooser's calm chassis and
// navigable ❯-rows: the gate states WHAT will run (action · subject · source, and
// for bash the sandbox confinement), then offers Allow once / Allow this session /
// Deny as highlightable rows. Risk is carried by the border colour and the
// highlighted default (Deny for destructive calls) — not by a permanently-loud
// frame — so the cyber identity lives on everywhere else while the gate itself
// reads calm and honest.
func (m chatTUI) renderApprovalBanner() string {
	w := m.width
	if w < 10 {
		w = 10
	}
	if m.pendingApproval == nil {
		return ""
	}
	toolName := m.pendingApproval.Tool
	name, detail := approvalToolDetails(toolName)
	destructive := approvalDestructive(toolName)

	var lines []string
	// Action line — the first thing the eye hits, no decorative chrome. A single
	// amber ⚠ marks destructive calls; benign reads get a calm copper ▸.
	action := fmt.Sprintf(i18n.M.ToolApprovalActionFmt, name)
	if destructive {
		lines = append(lines, yellow("⚠ ")+bold(action))
	} else {
		lines = append(lines, accent("▸ ")+action)
	}
	// Subject (the command or path) — the one fact about WHAT runs. Width-aware, and
	// for bash middle-clipped so the dangerous tail (&&, |, ; rm) stays visible.
	if subj := strings.TrimSpace(m.pendingApproval.Subject); subj != "" {
		avail := w - 6
		if avail < 12 {
			avail = 12
		}
		if toolName == "bash" {
			lines = append(lines, "  "+cyan(clampMiddle(subj, avail)))
		} else {
			lines = append(lines, "  "+cyan(clampPlain(subj, avail)))
		}
	}
	// Source / intent detail (dim).
	if label := m.subagentLabelFor(m.pendingApprovalParent); label != "" {
		lines = append(lines, "  "+dim("requested by "+label))
	}
	for _, d := range strings.Split(detail, "\n") {
		if d != "" {
			lines = append(lines, "  "+dim(d))
		}
	}
	// Sandbox confinement — bash only, text-token-first so it survives NO_COLOR.
	if toolName == "bash" {
		if sl := m.sandboxStatusLine(); sl != "" {
			lines = append(lines, "  "+sl)
		}
	}
	// Navigable choice rows; the cursor highlights the (risk-aware) default.
	lines = append(lines,
		"",
		rowLine(m.approvalCursor == 0, 1, "", i18n.M.ToolApprovalAllowOnce, false),
		rowLine(m.approvalCursor == 1, 2, "", i18n.M.ToolApprovalAllowSession, false),
		rowLine(m.approvalCursor == 2, 3, "", i18n.M.ToolApprovalDeny, false),
	)
	for i := range lines {
		lines[i] = m.clampBannerLine(lines[i])
	}
	return m.frameApproval(lines, destructive, w)
}

// frameApproval renders the gate in the shared terminal-native overlay style.
// Risk is carried by the action marker and default selection rather than a
// bordered floating card.
func (m chatTUI) frameApproval(lines []string, destructive bool, w int) string {
	return approvalBannerStyle.Width(w).Render(strings.Join(lines, "\n"))
}

// approvalDestructive reports whether a gated call can change state, so the gate
// defaults to Deny and wears the amber frame. Reads are benign; writers, exec,
// process control, and unknown (MCP) tools are treated as needing care.
func approvalDestructive(toolName string) bool {
	return toolCategory[toolName] != "read"
}

// sandboxStatusLine renders the session's bash confinement as ONE line, in three
// states so a permanently-unconfined platform reads as a steady notice rather than
// a red alarm: enforce (calm green), unavailable-on-this-OS (amber), or
// deliberately-off (red). "" when unknown (e.g. in tests).
func (m chatTUI) sandboxStatusLine() string {
	switch m.bashSandbox.state {
	case "enforce":
		net := "net off"
		if m.bashSandbox.network {
			net = "net on"
		}
		return green("sandbox: enforce · writes confined · " + net)
	case "unavailable":
		return yellow("sandbox: unavailable on this OS — runs unconfined")
	default:
		// "off", "" (unknown / config-load failure), or anything unrecognised — fail
		// SAFE and warn, never silently drop the confinement line on a bash gate.
		return red("UNCONFINED — full disk + network access")
	}
}

// bashSandboxStatus is the session's bash confinement summary (see chatTUI.bashSandbox).
type bashSandboxStatus struct {
	state   string // "enforce" | "unavailable" | "off"; "" = unknown (render nothing)
	network bool
}

// bashSandboxFromConfig derives the confinement summary from the configured bash
// mode and whether the platform can actually enforce it (sandbox.Available()).
func bashSandboxFromConfig(cfg *config.Config) bashSandboxStatus {
	st := bashSandboxStatus{network: cfg.Sandbox.Network}
	switch {
	case cfg.BashMode() == "enforce" && sandbox.Available():
		st.state = "enforce"
	case cfg.BashMode() == "enforce":
		st.state = "unavailable"
	default:
		st.state = "off"
	}
	return st
}

func (m chatTUI) clampBannerLine(s string) string {
	w := m.width - 2 // left padding plus a little guard against style wrapping
	if w < 8 {
		w = 8
	}
	return clampStatusLine(s, w)
}

// approvalToolDetails turns provider-visible tool IDs into user-facing labels.
// MCP tools are advertised as mcp__<server>__<tool>; showing the short tool name
// first keeps the approval prompt readable while preserving the source.
func approvalToolDetails(toolName string) (name, detail string) {
	if server, short, ok := tool.SplitMCPName(toolName); ok {
		lines := []string{}
		if strings.EqualFold(short, "understand_image") {
			lines = append(lines, i18n.M.ToolApprovalImageUse)
		}
		lines = append(lines, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, server))
		return short, strings.Join(lines, "\n")
	}
	return toolName, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, i18n.M.ToolApprovalBuiltIn)
}
