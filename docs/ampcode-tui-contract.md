# Ampcode-like TUI Contract

This document defines the visual contract for the `roach-code` chat TUI refresh.
It is intentionally about feel and structure, not Ampcode branding or keymaps.

## Non-goals

- Do not change the existing `roach-code` keymap.
- Do not copy Ampcode names, logos, marketing text, or proprietary UI details.
- Do not replace the current Bubble Tea / Lip Gloss architecture.

## Layout

- The chat surface is a full-screen alt-screen app.
- The top row is a single-line thread header for session identity: model, current
  directory, and context information when available.
- The main area is the transcript viewport.
- Transient panels are pinned above the composer and counted in the bottom row
  budget so they never collide with the transcript or footer.
- The composer stays at the bottom above a fixed footer.
- The footer is exactly two status rows: interaction/runtime state, then live
  data such as model, effort, cache, jobs, balance, and cost.

## Styling

- The default visual language is foreground-only. Terminal background cells are
  left to the user's terminal theme.
- Command menus, pickers, approval prompts, chooser prompts, task panels, and
  subagent panels are compact terminal-native overlays. They should use spacing,
  dim text, accent text, and selection markers instead of bordered floating
  cards or filled panels.
- User messages render as a simple label plus body.
- Tool calls render as compact tool rows. The summary row is one line; detailed
  output follows as a subdued continuation block only when needed.
- The palette is neutral with one primary accent. The current `amp` theme is the
  baseline.

## Stability

- Streaming output must not change the footer height.
- Opening or closing pinned panels must resize the transcript viewport through
  explicit row budgeting.
- Resizing common terminal dimensions such as 80x24 and 120x40 must keep the
  header, viewport, composer, and fixed footer coherent.
- Wide runes, paste folding, approval gates, slash completion, `@` completion,
  resume picking, jobs picking, and transcript selection must keep their existing
  behavior.
