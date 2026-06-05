package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"roach-code/internal/event"
)

type subagentRun struct {
	id         string
	name       string
	label      string
	prompt     string
	activity   string
	lineIdx    int
	started    time.Time
	toolCalls  int
	tokens     int
	background bool
}

func isSubagentRootTool(name string) bool {
	switch name {
	case "task", "explore", "research", "review", "security_review":
		return true
	default:
		return false
	}
}

func (m *chatTUI) ensureSubagentRuns() {
	if m.subagents == nil {
		m.subagents = make(map[string]*subagentRun)
	}
}

func (m *chatTUI) rememberSubagentDispatch(t event.Tool) {
	if t.ID == "" || !isSubagentRootTool(t.Name) {
		return
	}
	m.ensureSubagentRuns()
	if _, ok := m.subagents[t.ID]; ok {
		return
	}
	label := subagentLabel(t)
	m.subagents[t.ID] = &subagentRun{
		id:         t.ID,
		name:       t.Name,
		label:      label,
		prompt:     subagentPrompt(t),
		lineIdx:    len(m.transcript) - 1,
		started:    time.Now(),
		toolCalls:  0,
		background: subagentBackground(t),
	}
}

func (m *chatTUI) renderSubagentChildDispatch(t event.Tool) {
	if t.ParentID == "" {
		return
	}
	m.ensureSubagentRuns()
	run := m.subagents[t.ParentID]
	if run == nil {
		run = &subagentRun{
			id:      t.ParentID,
			name:    "task",
			label:   "subagent",
			lineIdx: -1,
			started: time.Now(),
		}
		m.subagents[t.ParentID] = run
	}
	run.toolCalls++
	run.activity = subagentActivity(t)
	m.clearReadRollupFor(t.ParentID)
	m.commitLine(subagentChildToolCard(t.Name, t.Args, m.width))
	m.beginToolRunningWithPrefix(t.ID, "  │ ")
}

func subagentChildToolCard(name, args string, width int) string {
	prefix := dim("  │ ") + toolDot(name) + " "
	reserved := len([]rune("  │ ● "))
	return clampStatusLine(prefix+toolHeadReserved(name, toolArg(name, args), width, reserved), width)
}

func (m *chatTUI) summarizeSubagentResult(t event.Tool) {
	if t.ID == "" || !isSubagentRootTool(t.Name) {
		return
	}
	m.ensureSubagentRuns()
	run := m.subagents[t.ID]
	if run == nil {
		run = &subagentRun{
			id:         t.ID,
			name:       t.Name,
			label:      subagentLabel(t),
			prompt:     subagentPrompt(t),
			lineIdx:    -1,
			started:    time.Now(),
			background: subagentBackground(t),
		}
		m.subagents[t.ID] = run
	}
	line := m.subagentSummaryLine(run, t)
	if run.lineIdx >= 0 && run.lineIdx < len(m.transcript) {
		m.replaceTranscriptLine(run.lineIdx, line)
	} else {
		m.commitLine(line)
	}
	delete(m.subagents, t.ID)
}

func (m *chatTUI) recordSubagentUsage(parentID string, totalTokens int) {
	if parentID == "" || totalTokens <= 0 {
		return
	}
	m.ensureSubagentRuns()
	run := m.subagents[parentID]
	if run == nil {
		run = &subagentRun{
			id:      parentID,
			name:    "task",
			label:   "subagent",
			lineIdx: -1,
			started: time.Now(),
		}
		m.subagents[parentID] = run
	}
	run.tokens += totalTokens
}

func (m *chatTUI) markSubagentAwaitingApproval(parentID string, a event.Approval) {
	if parentID == "" {
		return
	}
	m.ensureSubagentRuns()
	run := m.subagents[parentID]
	if run == nil {
		run = &subagentRun{
			id:      parentID,
			name:    "task",
			label:   "subagent",
			lineIdx: -1,
			started: time.Now(),
		}
		m.subagents[parentID] = run
	}
	subject := sanitizeSubagentText(a.Subject)
	if subject != "" {
		run.activity = "Awaiting approval " + toolDisplayName(a.Tool) + " " + subject
	} else {
		run.activity = "Awaiting approval " + toolDisplayName(a.Tool)
	}
}

func (m chatTUI) subagentLabelFor(parentID string) string {
	if parentID == "" || m.subagents == nil {
		return ""
	}
	if run := m.subagents[parentID]; run != nil {
		if run.label != "" {
			return run.label
		}
		return toolDisplayName(run.name)
	}
	return ""
}

func (m *chatTUI) subagentSummaryLine(run *subagentRun, t event.Tool) string {
	glyph := green("✔")
	status := "done"
	if t.Err != "" {
		glyph = red("✖")
		status = "failed"
		if isSubagentCancelled(t.Err) {
			glyph = yellow("✖")
			status = "cancelled"
		}
	} else if run.background {
		glyph = cyan("○")
		status = "background"
	}
	parts := []string{fmt.Sprintf("%d tools", run.toolCalls)}
	if !run.started.IsZero() {
		parts = append(parts, formatSubagentDuration(time.Since(run.started)))
	}
	if run.tokens > 0 {
		parts = append(parts, shortTokens(run.tokens)+" tokens")
	}
	if status != "done" {
		parts = append(parts, status)
	}
	if t.Err != "" {
		parts = append(parts, sanitizeSubagentText(t.Err))
	}
	tail := ""
	if len(parts) > 0 {
		tail = dim(" · " + joinSubagentParts(parts))
	}
	label := run.label
	if label == "" {
		label = toolDisplayName(run.name)
	}
	return clampStatusLine("  "+glyph+" "+bold(toolDisplayName(run.name))+cyan("(")+clampPlain(label, m.width-14)+magenta(")")+tail, m.width)
}

func isSubagentCancelled(errText string) bool {
	errText = strings.ToLower(errText)
	return strings.Contains(errText, "context canceled") || strings.Contains(errText, "cancelled") || strings.Contains(errText, "canceled")
}

func subagentLabel(t event.Tool) string {
	if arg := toolArg(t.Name, t.Args); arg != "" {
		return sanitizeSubagentText(arg)
	}
	var m map[string]any
	if json.Unmarshal([]byte(t.Args), &m) == nil {
		for _, key := range []string{"description", "task", "name", "arguments", "prompt"} {
			if v, ok := m[key].(string); ok && v != "" {
				return sanitizeSubagentText(v)
			}
		}
	}
	return toolDisplayName(t.Name)
}

func subagentPrompt(t event.Tool) string {
	var m map[string]any
	if json.Unmarshal([]byte(t.Args), &m) == nil {
		for _, key := range []string{"prompt", "task", "arguments"} {
			if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
				return sanitizeSubagentText(v)
			}
		}
	}
	return ""
}

func subagentBackground(t event.Tool) bool {
	var m map[string]any
	if json.Unmarshal([]byte(t.Args), &m) != nil {
		return false
	}
	v, _ := m["run_in_background"].(bool)
	return v
}

func subagentActivity(t event.Tool) string {
	if arg := toolArg(t.Name, t.Args); arg != "" {
		return toolDisplayName(t.Name) + " " + sanitizeSubagentText(arg)
	}
	return toolDisplayName(t.Name)
}

func sanitizeSubagentText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) {
					if s[i] >= 0x40 && s[i] <= 0x7e {
						i++
						break
					}
					i++
				}
				continue
			}
			if i < len(s) && s[i] == ']' {
				i++
				for i < len(s) {
					if s[i] == 0x07 {
						i++
						break
					}
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
			continue
		}
		r, size := rune(c), 1
		if c >= 0x80 {
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r == 0x7f || r < 0x20:
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func (m chatTUI) renderSubagentPanel() string {
	if len(m.subagents) == 0 {
		return ""
	}
	const maxRows = 12
	runs := make([]*subagentRun, 0, len(m.subagents))
	for _, run := range m.subagents {
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].started.Equal(runs[j].started) {
			return runs[i].id < runs[j].id
		}
		return runs[i].started.Before(runs[j].started)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", glitchMark("AGENTS"), dim(fmt.Sprintf("// %d running", len(m.subagents))))
	b.WriteString("  " + bold("main") + "\n")
	count := 0
	for _, run := range runs {
		if count >= maxRows {
			fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("  ^ %d more above (/jobs to view all)", len(m.subagents)-count)))
			break
		}
		count++
		elapsed := formatSubagentDuration(time.Since(run.started))
		label := run.label
		if label == "" {
			label = toolDisplayName(run.name)
		}
		line := "  " + yellow("○") + " " + bold(toolDisplayName(run.name)) + cyan("(") + clampPlain(label, 36) + magenta(")")
		if run.activity != "" {
			line += dim(" · " + clampPlain(run.activity, 32))
		}
		line += dim(" · " + elapsed)
		if run.tokens > 0 {
			line += dim(" · " + shortTokens(run.tokens) + " tokens")
		}
		b.WriteString(clampStatusLine(line, m.width))
		b.WriteString("\n")
	}
	return todoPanelStyle.Width(max(m.width, 10)).Render(strings.TrimRight(b.String(), "\n"))
}

func joinSubagentParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}

func formatSubagentDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	secs = secs % 60
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%02ds", mins, secs)
}
