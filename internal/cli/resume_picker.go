package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/agent"
	"roach-code/internal/i18n"
)

// resumePreviewMessages caps how many trailing user/assistant messages the
// picker renders for its live preview — enough to show where a conversation left
// off without rendering (and tailing) a possibly multi-MiB transcript per frame.
const resumePreviewMessages = 6

// resumePreviewLines caps the rendered preview height so a long final answer
// can't push the picker panel over the screen.
const resumePreviewLines = 12

// resumePicker is an in-chat overlay for "/resume" that lets the user pick a
// saved session by navigating with ↑/↓ and confirming with Enter. It mirrors
// the rewindPicker pattern: keys route through handleResumePickerKey and it
// renders via renderResumePicker while m.resumePick is set.
type resumePicker struct {
	sessions []agent.SessionInfo
	sel      int // selected index
	active   int // index of the currently-active session (-1 when none)

	// Preview of the highlighted session's tail, so its data shows on selection
	// rather than only after Enter. Cached by path+width because renderResumePicker
	// runs twice per frame (bottomRows + View) and we don't want to re-read disk or
	// re-render markdown each time; it only changes when the selection or width does.
	pvPath  string
	pvWidth int
	pvText  string
}

// openResumePicker populates the picker from the session directory and opens it.
// A no-op (with a notice) when there are no saved sessions.
func (m *chatTUI) openResumePicker() {
	sessions := recentSessions(m.ctrl.SessionDir())
	if len(sessions) == 0 {
		m.notice(i18n.M.NoSessionToResume)
		return
	}
	active := m.ctrl.SessionPath()
	activeIdx := -1
	for i, s := range sessions {
		if s.Path == active {
			activeIdx = i
			break
		}
	}
	// Default selection: the first session after the active one, else 0.
	sel := 0
	if activeIdx >= 0 && activeIdx+1 < len(sessions) {
		sel = activeIdx + 1
	}
	m.resumePick = &resumePicker{sessions: sessions, sel: sel, active: activeIdx}
}

func (m chatTUI) handleResumePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.resumePick
	if r == nil {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if r.sel > 0 {
			r.sel--
		}
	case "down", "j":
		if r.sel < len(r.sessions)-1 {
			r.sel++
		}
	case "enter":
		return m.applyResumePick()
	case "esc":
		m.resumePick = nil
	}
	return m, nil
}

func (m chatTUI) applyResumePick() (tea.Model, tea.Cmd) {
	r := m.resumePick
	if r == nil || r.sel < 0 || r.sel >= len(r.sessions) {
		return m, nil
	}
	target := r.sessions[r.sel]
	m.resumePick = nil
	if target.Path == m.ctrl.SessionPath() {
		m.notice(i18n.M.ResumeAlreadyActive)
		return m, nil
	}
	if m.ctrl.Running() {
		m.notice(i18n.M.ResumeBusy)
		return m, nil
	}
	loaded, err := agent.LoadSession(target.Path)
	if err != nil {
		m.notice("resume: " + err.Error())
		return m, nil
	}
	_ = m.ctrl.Snapshot()
	m.ctrl.Resume(loaded, target.Path)
	m.replayActiveBranch(i18n.M.ResumedTitle)
	return m, nil
}

func (m chatTUI) renderResumePicker() string {
	r := m.resumePick
	if r == nil {
		return ""
	}
	w := max(m.width, 10)
	var b strings.Builder
	b.WriteString(accent(i18n.M.ResumePickTitle) + "\n")
	for i, s := range r.sessions {
		label := sessionPickerLabel(s)
		if i == r.active {
			label = dim(label) + " " + dim("(active)")
		}
		b.WriteString(rowLine(i == r.sel, i+1, "", label, false) + "\n")
	}
	if preview := r.previewFor(r.sessions[r.sel], w, m.renderer); preview != "" {
		b.WriteString(dim(strings.Repeat("─", min(w-2, 40))) + "\n")
		b.WriteString(preview + "\n")
	}
	b.WriteString(dim(i18n.M.ResumePickHint))
	return choicePanelStyle.Width(w).Render(b.String())
}

// previewFor renders the tail of the given session's transcript for display in
// the picker. It reuses replaySectionsFor (the same user-bubble + assistant-
// markdown blocks resume uses) on only the last resumePreviewMessages so cost is
// bounded, then trims to resumePreviewLines. Results are cached by path+width;
// a load or render error yields "" so the picker simply shows the list alone.
func (r *resumePicker) previewFor(s agent.SessionInfo, width int, renderer *mdRenderer) string {
	if r.pvPath == s.Path && r.pvWidth == width {
		return r.pvText
	}
	r.pvPath, r.pvWidth, r.pvText = s.Path, width, sessionPreview(s.Path, width, renderer)
	return r.pvText
}

// sessionPreview renders the tail of a saved session's transcript — the same
// user-bubble + assistant-markdown blocks resume replays — bounded to the last
// resumePreviewMessages and resumePreviewLines so previewing a huge session stays
// cheap. A load error yields "" (callers then show no preview). It does no
// caching of its own; callers cache by path+width since it reads from disk.
func sessionPreview(path string, width int, renderer *mdRenderer) string {
	loaded, err := agent.LoadSession(path)
	if err != nil || loaded == nil {
		return ""
	}
	history := loaded.Messages
	if len(history) > resumePreviewMessages {
		history = history[len(history)-resumePreviewMessages:]
	}
	// Render inside the panel's content width (PaddingLeft(1), no side borders).
	sections := replaySectionsFor(history, max(width-2, 10), renderer)
	return tailLines(strings.TrimRight(strings.Join(sections, ""), "\n"), resumePreviewLines)
}

// tailLines keeps at most the last n lines of s, prefixing an ellipsis row when
// content was dropped so the preview reads as a continuation, not the whole thing.
func tailLines(s string, n int) string {
	if s == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return dim("  …") + "\n" + strings.Join(lines[len(lines)-n:], "\n")
}

// sessionPickerLabel is the "N turns · first message" line, truncated to fit.
func sessionPickerLabel(s agent.SessionInfo) string {
	preview := s.Preview
	if preview == "" {
		preview = "(no user message yet)"
	}
	return fmt.Sprintf("%d turns · %s", s.Turns, ansi.Truncate(preview, 60, "…"))
}
