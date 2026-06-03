# Goal Feature Design

`/goal` arms a session-scoped condition that keeps the controller working until the model reports the condition is met. The feature belongs in `control.Controller` so chat TUI, HTTP/SSE, and desktop frontends share one implementation.

## Runtime flow

1. `SetGoal` records the natural-language condition and resets loop counters.
2. At the tail of each normal turn, `runGoalLoop` checks the latest assistant text for a final `GOAL_STATUS` line.
3. If the status is `met`, the controller clears the goal and emits a notice.
4. If the status is `unmet`, the controller appends a tail-only continuation message and calls the runner again.
5. The loop stops when the goal is met, the user cancels, or a continuation adds no new messages.

The continuation message is appended at the turn tail so the cache-stable system prompt prefix remains unchanged.

## Approval behavior

Goal continuations use `goalAutoApp`, a dedicated temporary auto-approval flag. It is set only while a goal continuation is running and cleared before the next iteration or return. Deny rules still apply before approvals are auto-allowed.

## Frontend behavior

Frontends show a persistent goal chip with the current iteration count. The live status line describes the current goal pursuit and shows the model's last unmet-status nudge when available. `Esc`/cancel stops the current run without clearing the goal.

## Tests

- `TestGoalLoopContinuesUntilMet` verifies repeated continuations until a final `GOAL_STATUS: met`.
- `TestGoalLoopStopsOnStall` verifies the stall guard.
- `TestGoalAutoApproveIsolated` verifies `goalAutoApp` is set during a continuation and cleared afterward.
- Goal status parsing tests verify strict final-line handling and display stripping.
