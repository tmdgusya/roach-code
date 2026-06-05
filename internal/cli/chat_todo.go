package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// todoPanelMaxRows caps how many task lines the pinned panel shows; a long list
// is truncated with a "+N more" footer so the bottom region stays compact.
const todoPanelMaxRows = 8

type todoPanelItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
	Level      int    `json:"level"`
}

// renderTodoPanel renders the task list pinned above the input from the latest
// todo_write call (m.todoArgs): a "Tasks done/total" header, completed items
// dimmed/checked, the in-progress one highlighted (its activeForm if given),
// pending ones muted. It returns "" when there's no list or every item is done,
// so the panel appears while work is outstanding and clears itself when finished.
func (m chatTUI) renderTodoPanel() string {
	var p struct {
		Todos []todoPanelItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(m.todoArgs), &p); err != nil || len(p.Todos) == 0 {
		return ""
	}
	done := 0
	for _, t := range p.Todos {
		if t.Status == "completed" {
			done++
		}
	}
	if done == len(p.Todos) {
		return "" // all finished — clear the panel
	}

	innerWidth := max(12, m.width-1)
	var b strings.Builder
	header := accent("TASKS") + " " + dim(fmt.Sprintf("%d/%d", done, len(p.Todos)))
	if innerWidth >= 28 {
		header += " " + todoProgressBar(done, len(p.Todos), 12)
	}
	b.WriteString(clampStatusLine(header, innerWidth))
	b.WriteString("\n")
	shown := 0
	for i, t := range p.Todos {
		if shown >= todoPanelMaxRows {
			b.WriteString(clampStatusLine(dim(fmt.Sprintf("%s+%d more", todoOverflowPrefix(p.Todos, shown-1), len(p.Todos)-shown)), innerWidth))
			b.WriteString("\n")
			break
		}
		shown++
		indent := todoTreePrefix(p.Todos, i)
		labelBudget := max(8, innerWidth-visibleWidth(indent)-7)
		var line string
		switch t.Status {
		case "completed":
			line = dim(indent) + green("✓ ") + dim(clampPlain(t.Content, labelBudget))
		case "in_progress":
			label := t.Content
			if t.ActiveForm != "" {
				label = t.ActiveForm
			}
			line = accent(indent) + yellow("▶ ") + bold(clampPlain(label, labelBudget)) + " " + shimmer("now", m.shimmerPhase)
		default:
			line = dim(indent+"○ ") + clampPlain(t.Content, labelBudget)
		}
		b.WriteString(clampStatusLine(line, innerWidth))
		b.WriteString("\n")
	}
	return todoPanelStyle.Width(max(m.width, 10)).Render(strings.TrimRight(b.String(), "\n"))
}

func todoProgressBar(done, total, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	filled := done * width / total
	if done > 0 && filled == 0 {
		filled = 1
	}
	if done < total && filled == width {
		filled = width - 1
	}
	return dim("[") + green(strings.Repeat("●", filled)) + dim(strings.Repeat("·", width-filled)+"]")
}

func todoTreePrefix(items []todoPanelItem, idx int) string {
	level := max(0, items[idx].Level)
	var b strings.Builder
	b.WriteString("  ")
	for depth := 0; depth < level; depth++ {
		if todoAncestorHasLaterSibling(items, idx, depth) {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	if todoHasLaterSibling(items, idx, level) {
		b.WriteString("├─ ")
	} else {
		b.WriteString("╰─ ")
	}
	return b.String()
}

func todoOverflowPrefix(items []todoPanelItem, prevIdx int) string {
	if prevIdx < 0 || prevIdx >= len(items) {
		return "  ╰─ "
	}
	level := max(0, items[prevIdx].Level)
	return "  " + strings.Repeat("   ", level) + "╰─ "
}

func todoHasLaterSibling(items []todoPanelItem, idx, level int) bool {
	for j := idx + 1; j < len(items); j++ {
		lj := max(0, items[j].Level)
		if lj < level {
			return false
		}
		if lj == level {
			return true
		}
	}
	return false
}

func todoAncestorHasLaterSibling(items []todoPanelItem, idx, depth int) bool {
	for j := idx + 1; j < len(items); j++ {
		lj := max(0, items[j].Level)
		if lj < depth {
			return false
		}
		if lj == depth {
			return true
		}
	}
	return false
}
