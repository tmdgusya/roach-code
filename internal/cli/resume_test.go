package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/agent"
	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/provider"
)

// TestResumeDispatchOpensPicker proves bare "/resume" writes the session list
// to the scrollback (above the input) AND opens the interactive picker overlay.
func TestResumeDispatchOpensPicker(t *testing.T) {
	dir := t.TempDir()
	saveTestSession(t, filepath.Join(dir, "a.jsonl"), "alpha prompt")
	saveTestSession(t, filepath.Join(dir, "b.jsonl"), "beta prompt")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.width = 80
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	if cmd := m.runSlashCommand("/resume"); cmd != nil {
		t.Fatal("/resume should not return a tea.Cmd")
	}
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}
	if len(m.resumePick.sessions) != 2 {
		t.Fatalf("picker should have 2 sessions, got %d", len(m.resumePick.sessions))
	}
	// Session list must also appear in the scrollback transcript (above input).
	out := strings.Join(m.transcript, "\n")
	if !strings.Contains(out, "alpha prompt") || !strings.Contains(out, "beta prompt") {
		t.Fatalf("scrollback should contain session previews:\n%s", out)
	}
}

// TestResumePickerNavigateAndSelect proves the picker's up/down navigation and
// Enter to resume the selected session.
func TestResumePickerNavigateAndSelect(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	// Create two saved sessions.
	aPath := filepath.Join(dir, "a.jsonl")
	saveTestSession(t, aPath, "first session prompt")
	bPath := filepath.Join(dir, "b.jsonl")
	saveTestSession(t, bPath, "SECOND-SESSION-PROMPT")
	// Pin distinct mtimes so b is unambiguously the most recent. Created back to
	// back, the two files can land in the same filesystem mtime tick (seen on the
	// CI Windows runner), which then tie-breaks to a.jsonl by path and flakes.
	now := time.Now()
	if err := os.Chtimes(aPath, now.Add(-2*time.Second), now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bPath, now, now); err != nil {
		t.Fatal(err)
	}

	m := newTestChatTUI()
	m.width = 80
	m.ctrl = ctrl

	// Open the picker via bare /resume.
	m.runSlashCommand("/resume")
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}
	if len(m.resumePick.sessions) != 2 {
		t.Fatalf("picker should have 2 sessions, got %d", len(m.resumePick.sessions))
	}

	// The first session (default selection) is the most recent, which is b.jsonl.
	// Press Enter to resume it.
	next, _ := m.handleResumePickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)

	if got := ctrl.SessionPath(); got != bPath {
		t.Fatalf("session path = %q, want %q", got, bPath)
	}
	if out := strings.Join(m.transcript, "\n"); !strings.Contains(out, "SECOND-SESSION-PROMPT") {
		t.Fatalf("transcript should replay the resumed session:\n%s", out)
	}
	if m.resumePick != nil {
		t.Fatal("picker should close after resume")
	}
}

// TestResumePickerPreviewsOnSelection proves the picker renders the highlighted
// session's transcript immediately — before Enter — and that moving the selection
// swaps the preview to the newly highlighted session.
func TestResumePickerPreviewsOnSelection(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.jsonl")
	saveTestSession(t, aPath, "FIRST-SESSION-PREVIEW")
	bPath := filepath.Join(dir, "b.jsonl")
	saveTestSession(t, bPath, "SECOND-SESSION-PREVIEW")
	now := time.Now()
	if err := os.Chtimes(aPath, now.Add(-2*time.Second), now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bPath, now, now); err != nil {
		t.Fatal(err)
	}

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.width = 80
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.runSlashCommand("/resume")
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}

	// Default selection is the most recent session (b.jsonl); its data must show
	// in the rendered picker without pressing Enter.
	// The preview renders the message as a "You:" labelled block (amp-style); the
	// list shows it as a "N turns · …" label, so match the preview form.
	panel := m.renderResumePicker()
	if !strings.Contains(panel, "You: SECOND-SESSION-PREVIEW") {
		t.Fatalf("picker should preview the selected session immediately:\n%s", panel)
	}
	if strings.Contains(panel, "You: FIRST-SESSION-PREVIEW") {
		t.Fatalf("picker should not preview the unselected session:\n%s", panel)
	}
	// The controller must NOT have switched — preview is read-only until Enter.
	if got := m.ctrl.SessionPath(); got == bPath {
		t.Fatal("rendering the preview must not switch the active session")
	}

	// Moving the selection down swaps the preview to the other session.
	next, _ := m.handleResumePickerKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(chatTUI)
	panel = m.renderResumePicker()
	if !strings.Contains(panel, "FIRST-SESSION-PREVIEW") {
		t.Fatalf("preview should follow selection to the other session:\n%s", panel)
	}
}

// TestResumePickerEscDismisses proves pressing Esc closes the picker without
// switching sessions.
func TestResumePickerEscDismisses(t *testing.T) {
	dir := t.TempDir()
	saveTestSession(t, filepath.Join(dir, "a.jsonl"), "alpha prompt")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.runSlashCommand("/resume")
	if m.resumePick == nil {
		t.Fatal("bare /resume should open the picker")
	}

	next, _ := m.handleResumePickerKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(chatTUI)
	if m.resumePick != nil {
		t.Fatal("picker should close on Esc")
	}
}

// TestResumeDispatchSwitchesAndReplays drives "/resume <n>" through the slash
// dispatcher and asserts the controller switched session AND the resumed
// transcript was replayed into the scrollback.
func TestResumeDispatchSwitchesAndReplays(t *testing.T) {
	dir := t.TempDir()
	active := agent.NewSession("sys")
	active.Add(provider.Message{Role: provider.RoleUser, Content: "active prompt"})
	exec := agent.New(nil, nil, active, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})
	ctrl.SetSessionPath(filepath.Join(dir, "active.jsonl"))
	if err := ctrl.Snapshot(); err != nil {
		t.Fatal(err)
	}

	otherPath := filepath.Join(dir, "other.jsonl")
	saveTestSession(t, otherPath, "OTHER-SESSION-PROMPT")

	m := newTestChatTUI()
	m.width = 80
	m.ctrl = ctrl

	target := 0
	for i, s := range recentSessions(dir) {
		if s.Path == otherPath {
			target = i + 1
		}
	}
	if target == 0 {
		t.Fatal("other session not listed by recentSessions")
	}

	m.runSlashCommand("/resume " + strconv.Itoa(target))

	if got := ctrl.SessionPath(); got != otherPath {
		t.Fatalf("session path = %q, want %q", got, otherPath)
	}
	if out := strings.Join(m.transcript, "\n"); !strings.Contains(out, "OTHER-SESSION-PROMPT") {
		t.Fatalf("transcript should replay the resumed session:\n%s", out)
	}
}

func saveTestSession(t *testing.T, path, prompt string) {
	t.Helper()
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: prompt})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

// TestResumeArgCompletionListsSessions proves "/resume " opens an indexed menu
// of the saved sessions, mirroring the /switch branch completion.
func TestResumeArgCompletionListsSessions(t *testing.T) {
	dir := t.TempDir()
	saveTestSession(t, filepath.Join(dir, "a.jsonl"), "first")
	saveTestSession(t, filepath.Join(dir, "b.jsonl"), "second")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.input.SetValue("/resume ")
	m.updateCompletion()
	if !m.completion.active || m.completion.kind != compSlashArg {
		t.Fatalf("/resume should open argument completion: %+v", m.completion)
	}
	if got := labels(m.completion.items); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("resume completion = %v, want [1 2]", got)
	}
}

// TestResumeArgCompletionPreviewsSelection proves the "/resume <n>" completion
// menu renders the highlighted session's transcript inline, and that the preview
// follows the selection — so session data shows on selection, not only after the
// command runs.
func TestResumeArgCompletionPreviewsSelection(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.jsonl")
	saveTestSession(t, aPath, "FIRST-ARG-PREVIEW")
	bPath := filepath.Join(dir, "b.jsonl")
	saveTestSession(t, bPath, "SECOND-ARG-PREVIEW")
	now := time.Now()
	if err := os.Chtimes(aPath, now.Add(-2*time.Second), now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bPath, now, now); err != nil {
		t.Fatal(err)
	}

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.width = 80
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.input.SetValue("/resume ")
	m.updateCompletion()
	if !m.isResumeArgCompletion() {
		t.Fatalf("/resume should open the arg completion: %+v", m.completion)
	}

	// Index 1 (default selection) is the most recent session (b.jsonl): its data
	// must appear in the rendered menu without running the command. The preview
	// renders as a "You:" labelled block (amp-style); the list row uses "N turns · …".
	menu := m.renderCompletion()
	if !strings.Contains(menu, "You: SECOND-ARG-PREVIEW") {
		t.Fatalf("completion should preview the selected session:\n%s", menu)
	}
	if strings.Contains(menu, "You: FIRST-ARG-PREVIEW") {
		t.Fatalf("completion should not preview the unselected session:\n%s", menu)
	}

	// Moving down to index 2 swaps the preview to the other session.
	m.moveCompletion(1)
	menu = m.renderCompletion()
	if !strings.Contains(menu, "You: FIRST-ARG-PREVIEW") {
		t.Fatalf("preview should follow selection to the other session:\n%s", menu)
	}
}

// TestResumeArgCompletionEnterResumes proves that pressing Enter on a highlighted
// session in the "/resume" completion resumes it in a single press — even when its
// index (e.g. "1") is a prefix of another ("10"), which previously trapped Enter
// into endlessly re-accepting the number instead of running the command.
func TestResumeArgCompletionEnterResumes(t *testing.T) {
	dir := t.TempDir()
	// 11 sessions → the menu caps at 10, so indices run 1..10 and "1" is a prefix
	// of "10". newest is index 1.
	var newest string
	base := time.Now()
	for i := 0; i < 11; i++ {
		p := filepath.Join(dir, fmt.Sprintf("s%02d.jsonl", i))
		saveTestSession(t, p, fmt.Sprintf("SESSION-CONTENT-%02d", i))
		mt := base.Add(time.Duration(i) * time.Second) // later i = more recent
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		newest = p
	}

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	m.input.SetValue("/resume ")
	m.updateCompletion()
	if !m.isResumeArgCompletion() {
		t.Fatalf("expected /resume arg completion, got %+v", m.completion)
	}
	if len(m.completion.items) != resumeListCap {
		t.Fatalf("expected %d capped items, got %d", resumeListCap, len(m.completion.items))
	}

	// Default selection is index 1 (the newest session). One Enter resumes it.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)

	if got := ctrl.SessionPath(); got != newest {
		t.Fatalf("Enter should resume the highlighted session: path=%q want %q", got, newest)
	}
	if out := strings.Join(m.transcript, "\n"); !strings.Contains(out, "SESSION-CONTENT-10") {
		t.Fatalf("resumed transcript should replay the session:\n%s", out)
	}
	if m.completion.active {
		t.Fatal("completion should close after resuming")
	}
}

// TestResumeFromWelcomeShowsTranscript proves resuming straight from the fresh
// welcome screen (bannerLive=true) surfaces the replayed history: View renders the
// live banner instead of the viewport while bannerLive holds, so the replay must
// clear it or the resumed conversation stays invisible (the empty-screen bug).
func TestResumeFromWelcomeShowsTranscript(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "s.jsonl")
	saveTestSession(t, target, "WELCOME-RESUME-CONTENT")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	m.bannerLive = true // fresh welcome screen, as on a just-started session

	m.runResumeCommand("/resume 1")

	if m.bannerLive {
		t.Fatal("resume must freeze the live welcome banner so the transcript shows")
	}
	if out := strings.Join(m.transcript, "\n"); !strings.Contains(out, "WELCOME-RESUME-CONTENT") {
		t.Fatalf("resumed history should be in the transcript:\n%s", out)
	}
}

// TestResumeAcceptChainsIntoSessionMenu proves accepting "/resume" (a
// non-descend command that still takes arguments) immediately opens the session
// menu, rather than waiting for the next keystroke.
func TestResumeAcceptChainsIntoSessionMenu(t *testing.T) {
	dir := t.TempDir()
	saveTestSession(t, filepath.Join(dir, "a.jsonl"), "first")

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})

	m.input.SetValue("/resu")
	m.updateCompletion()
	m.acceptCompletion()
	if got := m.input.Value(); got != "/resume " {
		t.Fatalf("accepting /resume should fill %q, got %q", "/resume ", got)
	}
	if !m.completion.active || m.completion.kind != compSlashArg {
		t.Fatalf("accepting /resume should chain into the session menu: %+v", m.completion)
	}
}

// TestRunResumeSwitchesSession proves "/resume <n>" repoints the running
// controller to the chosen saved session and loads its history.
func TestRunResumeSwitchesSession(t *testing.T) {
	dir := t.TempDir()

	active := agent.NewSession("sys")
	active.Add(provider.Message{Role: provider.RoleUser, Content: "active prompt"})
	exec := agent.New(nil, nil, active, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, Label: "test"})
	activePath := filepath.Join(dir, "active.jsonl")
	ctrl.SetSessionPath(activePath)
	if err := ctrl.Snapshot(); err != nil {
		t.Fatal(err)
	}

	otherPath := filepath.Join(dir, "other.jsonl")
	saveTestSession(t, otherPath, "other prompt")

	m := newTestChatTUI()
	m.width = 80
	m.ctrl = ctrl

	target := 0
	for i, s := range recentSessions(dir) {
		if s.Path == otherPath {
			target = i + 1
		}
	}
	if target == 0 {
		t.Fatal("saved session not listed by recentSessions")
	}

	m.runResumeCommand("/resume " + strconv.Itoa(target))

	if got := ctrl.SessionPath(); got != otherPath {
		t.Fatalf("session path = %q, want %q", got, otherPath)
	}
	hist := ctrl.History()
	if len(hist) == 0 || hist[len(hist)-1].Content != "other prompt" {
		t.Fatalf("history not loaded from target: %+v", hist)
	}
}
