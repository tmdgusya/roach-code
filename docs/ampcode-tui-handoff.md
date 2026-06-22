# Handoff: Ampcode-like TUI refresh

## Task (one sentence)
Make the `roach-code` chat TUI visually and behaviorally feel close to Ampcode's modern terminal UI while keeping the existing `roach-code` command set and keymap.

## What "done" looks like (observable)
- Running `go test ./internal/cli` passes after adding or updating TUI rendering tests for the Ampcode-like layout.
- Rendering `chatTUI.View()` at common terminal sizes such as 80x24 and 120x40 shows a full-screen alt-screen layout with a single-line top thread header, a transcript viewport, compact terminal-native message/tool rows, pinned transient panels, a composer, and two fixed status rows.
- User messages render as a simple label plus body, without filled rounded-card styling or background SGR fills.
- Tool calls render as compact one-line summaries, with detailed output kept below as a subdued continuation block rather than a large card.
- The command/menu surfaces above the composer look like terminal-native overlays using foreground color, spacing, and selection, not bordered floating cards.
- The welcome/banner state, active turn state, approval/chooser state, completion menu, and resume/jobs picker state all fit the same visual language and do not shift the footer height during streaming.
- Existing behavior for slash commands, `@` references, approvals, `/resume`, `/jobs`, `/model`, `/theme`, pasted blocks, image paste, reasoning toggle, and transcript selection remains intact.

## In scope
- Define and enforce an Ampcode-like visual contract for the chat TUI.
- Keep the current Bubble Tea and Lip Gloss architecture.
- Keep the current full-screen alt-screen viewport model.
- Remove or reduce visual noise that makes the UI feel unlike Ampcode: heavy panels, card-like fills, excessive borders, warm/brand-heavy decoration, and verbose status text.
- Consolidate the visual hierarchy around:
  - top thread identity row,
  - main transcript viewport,
  - compact tool/reasoning/status rows,
  - pinned bottom panels,
  - composer,
  - two-row footer.
- Update tests around rendering invariants rather than terminal-specific screenshots where possible.
- Add a short design/implementation note documenting the TUI contract so future changes do not drift.

## Out of scope
- Changing the keymap to match Ampcode.
- Renaming `roach-code`, copying Ampcode branding, copying Ampcode text, or using Ampcode logos.
- Replacing Bubble Tea with a custom renderer.
- Rewriting agent, provider, tool, MCP, plugin, or session persistence logic.
- Adding new major product features such as remote control, web/mobile agents, or multi-agent sidebar behavior unless already present in the local TUI.
- Pixel-perfect or legally identical replication of Ampcode's proprietary UI.
- Broad refactors unrelated to the TUI rendering surface.

## Assumptions (recommended defaults, not confirmed decisions)
- Preserve the existing `roach-code` keymap and commands — recommended because the user explicitly said the Ampcode keymap is not needed.
- Aim for "same feel" through layout density, foreground-only styling, compact rows, and stable full-screen behavior rather than exact branding — recommended because this achieves the desired TUI experience without cloning protected identity.
- Treat the current `amp` theme as the baseline and refine it rather than adding another theme — recommended because `internal/cli/theme.go` already collapsed styles toward an Amp-like neutral palette.
- Use render/unit tests first, and only add heavier PTY/screenshot checks if a specific rendering issue cannot be pinned by string/ANSI assertions — recommended because the existing repo already has broad `internal/cli` render tests.

## Relevant context (from exploration)
- **Codebase area**:
  - `internal/cli/chat_tui.go`: owns the Bubble Tea model, full-screen `View()`, alt-screen flag, viewport sizing, mouse handling, composer, pinned panels, and footer.
  - `internal/cli/chat_status.go`: renders thread header, model/context/cache/cost/job status tags, and status row helpers.
  - `internal/cli/toolcard.go`: renders compact tool summaries and continuation blocks.
  - `internal/cli/theme.go`: defines the current `amp` dark/light palettes and foreground colors.
  - `internal/cli/complete.go`: renders slash and `@` completion data and drives completion state.
  - `internal/cli/chooser.go`, `internal/cli/resume_picker.go`, `internal/cli/jobs_picker.go`, `internal/cli/chat_approval.go`, `internal/cli/chat_subagent.go`: render transient panels pinned above the composer.
  - `internal/textarea/textarea.go`: composer implementation and sizing behavior.
- **Structure / conventions**:
  - The TUI is a Bubble Tea model using `charm.land/bubbletea/v2`, `bubbles/v2`, and `lipgloss/v2`.
  - `View()` returns a `tea.View` with `AltScreen = true` and `MouseModeCellMotion`.
  - The central transcript is wrapped into a `viewport.Model`; bottom-region rows are counted separately so footer/panels stay pinned.
  - Rendering tests usually assert ANSI-stripped text, line counts, row placement, and absence/presence of specific SGR sequences.
  - Existing comments still include some older normal-buffer wording; implementation is now effectively full-screen alt-screen.
- **Prior art**:
  - `internal/cli/amp_style_test.go` already asserts Amp-style user bubble, thread header, compact tool summary, and layout tail rows.
  - `toolCard()` already renders one-line summaries with `◐ Verb(arg)`.
  - `theme.go` already defines a single neutral `amp` theme in dark and light modes.
  - `View()` already places header, transcript, pinned bottom panels, composer, and two status rows in a stable layout.
- **Observable entry points**:
  - `go test ./internal/cli`
  - Focused tests such as `go test ./internal/cli -run 'Test(RenderUserBubble|RenderThreadHeader|RenderToolSummary|ViewLayout|TranscriptViewportSizing|BottomRows)'`
  - Manual run: `go run ./cmd/roach-code chat` in terminals at 80x24 and 120x40.

## Implementation plan
1. Write a TUI contract document, likely `docs/ampcode-tui-contract.md`, describing the visual rules: full-screen alt-screen, foreground-only styling, compact tool rows, subdued overlays, fixed footer, stable row budgeting, and no Ampcode keymap requirement.
2. Tighten tests in `internal/cli/amp_style_test.go` and adjacent layout tests so they pin the visual invariants:
   - first non-empty row is thread header,
   - final non-empty rows are fixed status rows,
   - user messages have no background fill,
   - tool summaries are single-line compact rows,
   - transient panels do not introduce wrapped footer/status rows,
   - command/completion surfaces render as compact overlays.
3. Normalize full-screen architecture comments and code boundaries in `chat_tui.go` so implementation and documentation agree that alt-screen owns the visible grid.
4. Audit visible surfaces and remove card-like styling where it conflicts with the contract:
   - approval banner,
   - chooser,
   - completion menu,
   - resume picker,
   - jobs picker,
   - subagent panel,
   - todo panel,
   - folded paste block.
5. Consolidate status/header content:
   - keep thread identity at the top,
   - keep runtime state and live data in the fixed footer,
   - trim verbose shortcut/help text from always-visible rows.
6. Keep the existing `roach-code` keymap unchanged, but make existing command/menu surfaces visually match the contract.
7. Run focused `internal/cli` tests, then full `go test ./internal/cli`, then broader `go test ./...` if the changes touch shared helpers.
8. Do a manual terminal smoke test for streaming, slash completion, `@` completion, approval, `/resume`, `/jobs`, paste folding, and resize behavior.

## Open risks
- Ampcode's exact TUI implementation is proprietary, so the target must be a documented "feel" rather than an exact clone.
- Some current UI surfaces may rely on borders/backgrounds for clarity; removing them may require compensating with spacing, dim text, and selection markers.
- Bubble Tea terminal rendering varies by terminal, especially around mouse mode, wide runes, IME cursor placement, and resize; manual checks in at least one real terminal remain necessary.
- Current tests assert some existing strings and row placement; tightening the visual contract may require updating tests that encode the old presentation.

## Confirmed by user
- [ ] User confirmed this handoff
