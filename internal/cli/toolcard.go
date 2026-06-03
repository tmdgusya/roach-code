// Formats a tool call as a Claude-style card line: a "● Verb(primary arg)"
// header instead of the raw "-> name {json}", plus the "╰─" continuation gutter.
package cli

import (
	"encoding/json"
	"strconv"
	"strings"

	"roach-code/internal/tool"
)

// gridIndent is the single content axis: structure (rail/gutter/dot) lives in
// columns 0–3, and every piece of content — tool verb, answer body, reasoning,
// tool output — begins at column 4, so the whole session hangs off one vertical
// spine instead of a ragged set of indents.
const gridIndent = 4

// connector is the left rail that ties a continuation block (tool output) to the
// dot above it: "  │ " puts the rail at column 2 and its text at the column-4
// axis, so the dot and its output share one continuous vertical stroke.
const connector = "  │ "

// surfaceWrap is intentionally a NO-OP: tool / output / diff blocks are rendered
// terminal-native, on the user's own terminal background, with only their TEXT
// coloured. It used to paint a warm "surface" fill behind these blocks, but any
// painted background fought the terminal — it floated as a panel of a different
// colour from everything around it, and the whole-screen fill we added to hide that
// caused black/grey boxes on terminals whose default background isn't our ink. We
// keep the function (and its callers) so the panel fill can be reintroduced as a
// deliberate, opt-in choice later, but by default it changes nothing. Diff add/remove
// bands keep their own meaningful green/red backgrounds — those are applied in
// diffview, not here.
func surfaceWrap(s string, _ int) string {
	return s
}

// connectorBlock renders lines under the connector: the first carries the rail
// gutter, the rest align beneath it. Returns "" for no lines.
func connectorBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	indent := strings.Repeat(" ", len([]rune(connector)))
	out := dim(connector) + lines[0]
	for _, ln := range lines[1:] {
		out += "\n" + indent + ln
	}
	return out
}

// toolVerb maps a tool's snake_case id to the verb shown in its card.
var toolVerb = map[string]string{
	"bash":          "Bash",
	"bash_output":   "Output",
	"kill_shell":    "Kill",
	"wait":          "Wait",
	"read_file":     "Read",
	"write_file":    "Write",
	"edit_file":     "Update",
	"multi_edit":    "Update",
	"delete_range":  "Update",
	"delete_symbol": "Update",
	"notebook_edit": "Update",
	"glob":          "Glob",
	"grep":          "Search",
	"ls":            "List",
	"web_fetch":     "Fetch",
	"web_search":    "Search",
	"complete_step": "Step",
	"task":          "Task",
}

// toolArgKey is the JSON field shown in parentheses for each tool (wait is
// special-cased — it carries a job_ids array, not a scalar).
var toolArgKey = map[string]string{
	"bash":          "command",
	"bash_output":   "job_id",
	"kill_shell":    "job_id",
	"read_file":     "path",
	"write_file":    "path",
	"edit_file":     "path",
	"multi_edit":    "path",
	"delete_range":  "path",
	"delete_symbol": "name",
	"notebook_edit": "path",
	"glob":          "pattern",
	"grep":          "pattern",
	"ls":            "path",
	"web_fetch":     "url",
	"web_search":    "query",
	"complete_step": "summary",
	"task":          "description",
}

// toolDot returns the "●" status glyph coloured by the tool's category so the eye
// can tell reads (cyan) from writes (green), shell (yellow), process control
// (magenta), and everything else (copper) at a glance.
func toolDot(name string) string {
	var c cliColor
	switch toolCategory[name] {
	case "read":
		c = activeCLITheme.toolRead
	case "write":
		c = activeCLITheme.success
	case "exec":
		c = activeCLITheme.warn
	case "proc":
		c = activeCLITheme.toolProc
	default:
		c = activeCLITheme.accent
	}
	return themeFg(c, "●")
}

var toolCategory = map[string]string{
	"read_file": "read", "ls": "read", "glob": "read", "grep": "read",
	"web_fetch": "read", "web_search": "read", "bash_output": "read",
	"write_file": "write", "edit_file": "write", "multi_edit": "write",
	"delete_range": "write", "delete_symbol": "write", "notebook_edit": "write",
	"bash": "exec",
	"wait": "proc", "kill_shell": "proc",
}

// toolDisplayName returns the card verb for a tool: a mapped builtin verb, the
// short name for an MCP tool (mcp__server__tool), or the raw id as a fallback.
func toolDisplayName(name string) string {
	if _, short, ok := tool.SplitMCPName(name); ok {
		return short
	}
	if v, ok := toolVerb[name]; ok {
		return v
	}
	return name
}

// toolArg pulls the primary argument shown in the card's parentheses.
func toolArg(name, args string) string {
	var m map[string]any
	if json.Unmarshal([]byte(args), &m) != nil {
		return ""
	}
	if name == "wait" {
		return argList(m["job_ids"])
	}
	v, ok := m[toolArgKey[name]]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		return argList(x)
	case float64:
		return strconv.Itoa(int(x))
	default:
		return ""
	}
}

func argList(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// toolCard renders the dispatch line: "  ● Verb(arg)" — the dot sits at column 2
// (structure) and the verb starts at the column-4 axis, with the tool's "  │ "
// output rail hanging straight off the same dot. Fewer glyphs than the old "╭─ ●"
// corner, more order.
func toolCard(name, args string, width int) string {
	prefix := "  " + toolDot(name) + " "
	reserved := len([]rune("  ● ")) // dot at col 2, verb at the col-4 axis
	return clampStatusLine(prefix+toolHeadReserved(name, toolArg(name, args), width, reserved), width)
}

// toolHead builds "Verb(arg)" with the verb bold and the arg clamped to fit the
// remaining width; shared by toolCard and the diff block header.
func toolHead(name, arg string, width int) string {
	return toolHeadReserved(name, arg, width, 4)
}

func toolHeadReserved(name, arg string, width, reserved int) string {
	label := toolDisplayName(name)
	head := bold(label)
	if arg != "" {
		avail := width - reserved - len([]rune(label)) - 2
		head += cyan("(") + clampPlain(arg, avail) + magenta(")")
	}
	return head
}
