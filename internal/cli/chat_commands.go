package cli

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/i18n"
	"roach-code/internal/outputstyle"
)

// runSlashCommand handles "/<cmd> <args>" input. Local commands queue their
// output to scrollback; MCP prompt / custom commands resolve to a model turn.
func (m *chatTUI) runSlashCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(strings.SplitN(input, " ", 2)[0])

	if strings.HasPrefix(cmd, "/mcp__") {
		return m.runMCPPrompt(input)
	}

	switch cmd {
	case "/compact":
		m.echoLocalCommand(input)
		// Compaction makes a (network) summarizer call; run it off the Update loop
		// so the TUI doesn't freeze. The CompactionStarted/Done events render the
		// card as they arrive; compactDoneMsg only handles the terminal error /
		// snapshot once the pass returns. Any text after "/compact" is focus
		// guidance steering what the summary keeps.
		focus := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		return func() tea.Msg { return compactDoneMsg{err: m.ctrl.Compact(context.Background(), focus)} }
	case "/new":
		m.echoLocalCommand(input)
		if err := m.ctrl.NewSession(); err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, err))
			return nil
		}
		// Native scrollback keeps the old transcript; mark the fork with a fresh
		// banner and reset live state.
		m.pending.Reset()
		m.reasoning.Reset()
		m.todoArgs = ""
		m.chooser = nil
		m.commitLine("")
		m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
		m.notice(i18n.M.SlashNewDone)
	case "/resume":
		m.runResumeCommand(input)
	case "/todo":
		m.echoLocalCommand(input)
		// Dismiss the pinned task list; a later todo_write brings it back.
		m.todoArgs = ""
		m.notice(i18n.M.SlashTodoCleared)
	case "/verbose":
		m.toggleVerboseReasoning(true)
	case "/effort":
		return m.runEffortCommand(input)
	case "/rewind":
		m.echoLocalCommand(input)
		m.openRewind()
	case "/tree":
		m.echoLocalCommand(input)
		m.showBranchTree()
	case "/branch":
		m.echoLocalCommand(input)
		m.runBranchCommand(input)
	case "/switch":
		m.echoLocalCommand(input)
		m.runSwitchCommand(input)
	case "/mcp":
		m.echoLocalCommand(input)
		m.runMCPSubcommand(input)
	case "/model":
		m.echoLocalCommand(input)
		m.runModelSubcommand(input)
		if m.pendingModelSwitch != nil {
			return m.pendingModelSwitch
		}
	case "/skill", "/skills":
		m.echoLocalCommand(input)
		m.runSkillSubcommand(input)
	case "/hooks":
		m.echoLocalCommand(input)
		m.runHooksSubcommand(input)
	case "/paste-image":
		return pasteClipboardImage()
	case "/output-style", "/output-styles":
		m.echoLocalCommand(input)
		styles := outputstyle.List(outputstyle.Dirs())
		if len(styles) == 0 {
			m.notice(i18n.M.OutputStyleNone)
		} else {
			m.commitLine(renderOutputStyles(m.width, styles, m.outputStyle))
		}
	case "/theme":
		m.echoLocalCommand(input)
		m.runThemeSubcommand(input)
	case "/help":
		m.echoLocalCommand(input)
		m.showHelp()
	case "/memory":
		m.echoLocalCommand(input)
		m.showMemory()
	case "/quit", "/exit":
		return tea.Quit
	case "/forget":
		m.forgetMemory(strings.TrimSpace(strings.TrimPrefix(input, cmd)))
	case "/goal":
		arg := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		switch {
		case arg == "":
			m.echoLocalCommand(input)
			if g, n := m.ctrl.Goal(); g != "" {
				m.notice(fmt.Sprintf("● goal active (×%d): %s", n, g))
			} else {
				m.notice(i18n.M.GoalNone)
			}
		case arg == "clear" || arg == "off":
			m.echoLocalCommand(input)
			if g, _ := m.ctrl.Goal(); g != "" {
				m.ctrl.ClearGoal()
				m.notice(i18n.M.GoalCleared)
			} else {
				m.notice(i18n.M.GoalNone)
			}
		default:
			// Arm the goal, then kick the first turn (unless one is already running —
			// then the in-flight turn picks it up at its tail). The bare condition is
			// the model's opening task; runGoalLoop takes over from the turn's end.
			m.ctrl.SetGoal(arg)
			m.notice(fmt.Sprintf(i18n.M.GoalSetFmt, arg))
			if !m.ctrl.Running() {
				return m.startTurn(arg, input, input)
			}
			m.echoLocalCommand(input)
		}
	default:
		// A custom command wins over a skill of the same name; both resolve to a turn.
		if sent, ok := m.ctrl.CustomCommand(input); ok {
			return m.startTurn(sent, input, input)
		}
		if sent, ok := m.ctrl.RunSkill(input); ok {
			return m.startTurn(sent, input, input)
		}
		m.notice(fmt.Sprintf("%s: %s", i18n.M.SlashUnknown, cmd))
	}
	return nil
}

func (m *chatTUI) echoLocalCommand(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	m.commitLine(dim("  › " + input))
}

// commandNames renders the custom command list for /help, "" when there are none.
func (m *chatTUI) commandNames() string {
	if len(m.commands) == 0 {
		return ""
	}
	names := make([]string, len(m.commands))
	for i, c := range m.commands {
		names[i] = "/" + c.Name
	}
	return strings.Join(names, " · ")
}

// runMCPSubcommand handles "/mcp" (status), "/mcp add …" (connect a server live
// and persist it), and "/mcp remove <name>" (disconnect + drop from config). Add
// connects synchronously — like /compact, an explicit command may briefly block
// the UI while the handshake runs.
func (m *chatTUI) runMCPSubcommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/mcp"
	if len(args) < 2 {
		m.showMCPStatus()
		return
	}
	switch args[1] {
	case "list", "ls":
		// The completion menu offers "list"; treat it as the status view (same as
		// a bare /mcp) rather than an unknown subcommand.
		m.showMCPStatus()
	case "add":
		entry, err := parseMCPAdd(args[2:])
		if err != nil {
			m.notice(err.Error())
			return
		}
		n, err := m.ctrl.AddMCPServer(entry)
		if err != nil {
			m.notice("mcp add: " + err.Error())
			return
		}
		m.notice(fmt.Sprintf("connected %s — %d tools, saved to config (available next message)", entry.Name, n))
	case "connect":
		if len(args) < 3 {
			m.notice("usage: /mcp connect <name>")
			return
		}
		n, err := m.ctrl.ConnectConfiguredMCPServer(args[2])
		if err != nil {
			m.notice("mcp connect: " + err.Error())
			return
		}
		m.host = m.ctrl.Host()
		m.notice(fmt.Sprintf("connected %s — %d tools (available next message)", args[2], n))
	case "remove", "rm":
		if len(args) < 3 {
			m.notice("usage: /mcp remove <name>")
			return
		}
		name := args[2]
		disconnected, err := m.ctrl.RemoveMCPServer(name)
		if err != nil {
			m.notice("mcp remove: " + err.Error())
			return
		}
		if disconnected {
			m.notice("disconnected " + name + " and removed it from config")
		} else {
			m.notice("removed " + name + " from config")
		}
	default:
		m.notice("unknown /mcp subcommand " + args[1] + " — try: /mcp, /mcp add, /mcp connect, /mcp remove")
	}
}

// showMCPStatus queues the connected MCP servers, their counts, and the prompt
// commands / resource refs they expose — the discovery surface for /mcp.
func (m *chatTUI) showMCPStatus() {
	if m.host == nil || (len(m.host.Servers()) == 0 && len(m.host.Failures()) == 0) {
		m.notice(i18n.M.SlashMCPNone)
		return
	}
	m.commitLine(renderMCPStatus(m.width, m.host.Servers(), m.host.Prompts(), m.host.Resources(), m.host.Failures()))
}

// notice queues a dim informational line to scrollback.
func (m *chatTUI) notice(note string) {
	m.commitLine(dim("  · " + note))
}

// resolveRefs resolves a line's @references off the event loop via the
// controller, delivering a refsResolvedMsg with the tagged context block.
func (m *chatTUI) resolveRefs(sent, display, restore string) tea.Cmd {
	return func() tea.Msg {
		block, errs := m.ctrl.ResolveRefs(context.Background(), sent)
		return refsResolvedMsg{sent: sent, display: display, restore: restore, block: block, errs: errs}
	}
}

// runMCPPrompt resolves a /mcp__server__prompt command off the event loop via
// the controller, delivering a promptResolvedMsg with the rendered prompt.
func (m *chatTUI) runMCPPrompt(input string) tea.Cmd {
	return func() tea.Msg {
		sent, found, err := m.ctrl.MCPPrompt(context.Background(), input)
		if !found {
			name := strings.TrimPrefix(strings.Fields(input)[0], "/")
			return promptResolvedMsg{display: input, err: fmt.Errorf("%s: /%s", i18n.M.SlashUnknown, name)}
		}
		return promptResolvedMsg{display: input, sent: sent, err: err}
	}
}
