package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// todoPanelMaxRows caps how many task lines the pinned panel shows; a long list
// is truncated with a "+N more" footer so the bottom region stays compact.
const todoPanelMaxRows = 8

// renderTodoPanel renders the task list pinned above the input from the latest
// todo_write call (m.todoArgs): a "Tasks done/total" header, completed items
// dimmed/checked, the in-progress one highlighted (its activeForm if given),
// pending ones muted. It returns "" when there's no list or every item is done,
// so the panel appears while work is outstanding and clears itself when finished.
func (m chatTUI) renderTodoPanel() string {
	var p struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
			Level      int    `json:"level"`
		} `json:"todos"`
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

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", glitchMark("TODO-STACK"), dim(fmt.Sprintf("// %d/%d", done, len(p.Todos))))
	shown := 0
	for _, t := range p.Todos {
		if shown >= todoPanelMaxRows {
			b.WriteString(dim(fmt.Sprintf("  +%d more", len(p.Todos)-shown)))
			b.WriteString("\n")
			break
		}
		shown++
		indent := "  "
		if t.Level >= 1 {
			indent = "      " // sub-steps sit under their phase
		}
		switch t.Status {
		case "completed":
			b.WriteString(indent)
			b.WriteString(green("✔"))
			b.WriteString(" ")
			b.WriteString(dim(t.Content))
			b.WriteString("\n")
		case "in_progress":
			label := t.Content
			if t.ActiveForm != "" {
				label = t.ActiveForm
			}
			b.WriteString(indent)
			b.WriteString(yellow("▶ " + label))
			b.WriteString("\n")
		default:
			b.WriteString(indent)
			b.WriteString(dim("○ " + t.Content))
			b.WriteString("\n")
		}
	}
	return todoPanelStyle.Width(max(m.width, 10)).Render(strings.TrimRight(b.String(), "\n"))
}
