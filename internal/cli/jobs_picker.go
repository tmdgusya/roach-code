package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/jobs"
)

type jobsPicker struct {
	sel         int
	detail      string
	pendingStop string
}

func (m *chatTUI) openJobsPicker() {
	if len(m.taskPickerEntries()) == 0 {
		m.notice("No background tasks.")
		return
	}
	m.jobsPick = &jobsPicker{}
}

type taskPickerEntry struct {
	id         string
	kind       string
	label      string
	status     string
	started    int64
	subagent   *subagentRun
	background *jobs.View
}

func (m chatTUI) taskPickerEntries() []taskPickerEntry {
	var entries []taskPickerEntry
	if len(m.subagents) > 0 {
		runs := make([]*subagentRun, 0, len(m.subagents))
		for _, run := range m.subagents {
			runs = append(runs, run)
		}
		sort.SliceStable(runs, func(i, j int) bool {
			return runs[i].started.After(runs[j].started)
		})
		for _, run := range runs {
			status := "running"
			if run.background {
				status = "background"
			}
			entries = append(entries, taskPickerEntry{
				id: run.id, kind: run.name, label: run.label, status: status,
				started: run.started.UnixMilli(), subagent: run,
			})
		}
	}
	if m.ctrl != nil {
		for _, j := range m.ctrl.AllJobs() {
			jj := j
			entries = append(entries, taskPickerEntry{
				id: j.ID, kind: j.Kind, label: j.Label, status: j.Status,
				started: j.StartedAt, background: &jj,
			})
		}
	}
	return entries
}

func (m chatTUI) handleJobsPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.jobsPick
	if p == nil {
		return m, nil
	}
	entries := m.taskPickerEntries()
	if len(entries) == 0 {
		m.jobsPick = nil
		m.notice("No background tasks.")
		return m, nil
	}
	if p.sel >= len(entries) {
		p.sel = len(entries) - 1
	}
	switch msg.String() {
	case "up", "k":
		if p.sel > 0 {
			p.sel--
			p.detail = ""
			p.pendingStop = ""
		}
	case "down", "j":
		if p.sel < len(entries)-1 {
			p.sel++
			p.detail = ""
			p.pendingStop = ""
		}
	case "enter", "o":
		p.detail = m.taskPickerDetail(entries[p.sel])
		p.pendingStop = ""
	case "x", "delete", "backspace":
		p.detail = m.stopTaskPickerEntry(entries[p.sel], p)
	case "r":
		p.detail = ""
		p.pendingStop = ""
	case "esc":
		m.jobsPick = nil
	}
	return m, nil
}

func (m chatTUI) renderJobsPicker() string {
	p := m.jobsPick
	if p == nil {
		return ""
	}
	entries := m.taskPickerEntries()
	if len(entries) == 0 {
		return ""
	}
	if p.sel >= len(entries) {
		p.sel = len(entries) - 1
	}
	w := max(m.width, 10)
	var b strings.Builder
	b.WriteString(accent("Background tasks") + "\n")
	for i, e := range entries {
		b.WriteString(rowLine(i == p.sel, i+1, "", taskPickerLabel(e), false) + "\n")
	}
	if strings.TrimSpace(p.detail) != "" {
		for _, line := range strings.Split(strings.TrimSpace(p.detail), "\n") {
			b.WriteString(dim("  "+clampPlain(line, max(1, m.width-4))) + "\n")
		}
	}
	b.WriteString(dim("↑↓ navigate · Enter detail · x stop · r refresh · Esc back"))
	return choicePanelStyle.Width(w).Render(strings.TrimRight(b.String(), "\n"))
}

func taskPickerLabel(e taskPickerEntry) string {
	label := e.id
	if e.label != "" {
		label += " (" + e.label + ")"
	}
	return fmt.Sprintf("%s · %s · %s", label, e.kind, e.status)
}

func (m chatTUI) taskPickerDetail(e taskPickerEntry) string {
	if e.background != nil {
		if m.ctrl == nil {
			return "background jobs are not available"
		}
		return m.ctrl.JobsText("/jobs output " + e.id)
	}
	if e.subagent == nil {
		return ""
	}
	run := e.subagent
	parts := []string{
		taskPickerLabel(e),
		"elapsed: " + formatSubagentDuration(time.Since(run.started)),
		fmt.Sprintf("tools: %d", run.toolCalls),
	}
	if run.tokens > 0 {
		parts = append(parts, "tokens: "+shortTokens(run.tokens))
	}
	if run.activity != "" {
		parts = append(parts, "activity: "+run.activity)
	}
	if run.prompt != "" {
		parts = append(parts, "prompt: "+run.prompt)
	}
	if !run.background {
		parts = append(parts, "stop: press x twice to stop the parent turn")
	}
	return strings.Join(parts, "\n")
}

func (m *chatTUI) stopTaskPickerEntry(e taskPickerEntry, p *jobsPicker) string {
	if e.background != nil {
		if m.ctrl == nil {
			return "background jobs are not available"
		}
		p.pendingStop = ""
		return m.ctrl.JobsText("/jobs kill " + e.id)
	}
	if e.subagent == nil {
		p.pendingStop = ""
		return ""
	}
	if e.subagent.background {
		p.pendingStop = ""
		return "Background subagent is managed by its job entry."
	}
	if p.pendingStop != e.id {
		p.pendingStop = e.id
		return "Stopping this foreground subagent will cancel the parent turn. Press x again to stop."
	}
	p.pendingStop = ""
	if m.ctrl != nil {
		m.ctrl.Cancel()
	}
	return "Stopped foreground subagent by cancelling the parent turn."
}
