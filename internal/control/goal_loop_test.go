package control

import (
	"context"
	"strings"
	"testing"

	"roach-code/internal/agent"
	"roach-code/internal/event"
	"roach-code/internal/provider"
)

// scriptedGoalRunner mocks the agent runner for goal-loop tests: each Run records
// its input and appends the next scripted assistant reply (a "" reply appends
// nothing — used to simulate a no-progress iteration for the stall detector).
type scriptedGoalRunner struct {
	session *agent.Session
	replies []string
	inputs  []string
	calls   int
	onRun   func(input string)
}

func (r *scriptedGoalRunner) Run(ctx context.Context, input string) error {
	return r.RunMessage(ctx, provider.Message{Role: provider.RoleUser, Content: input})
}

func (r *scriptedGoalRunner) RunMessage(_ context.Context, msg provider.Message) error {
	input := msg.Content
	r.inputs = append(r.inputs, input)
	if r.onRun != nil {
		r.onRun(input)
	}
	i := r.calls
	r.calls++
	if i < len(r.replies) && r.replies[i] != "" {
		r.session.Add(provider.Message{Role: provider.RoleAssistant, Content: r.replies[i]})
	}
	return nil
}

// TestGoalLoopRunsUntilMet drives the loop through two unmet continuations to a met
// verdict, asserting it auto-clears and that the continuation rode the raw framed
// prompt (cache-stable: a tail append, not a Composed message).
func TestGoalLoopRunsUntilMet(t *testing.T) {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "starting\nGOAL_STATUS: unmet — step one"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &scriptedGoalRunner{session: sess, replies: []string{
		"did one\nGOAL_STATUS: unmet — step two",
		"did two\nGOAL_STATUS: met — all green",
	}}
	c := New(Options{Runner: runner, Executor: exec, Label: "test"})
	c.SetGoal("make it green")

	if err := c.runGoalLoop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("continuations = %d, want 2", runner.calls)
	}
	if g, n := c.Goal(); g != "" || n != 0 {
		t.Fatalf("goal should auto-clear, got %q ×%d", g, n)
	}
	if len(runner.inputs) == 0 ||
		!strings.Contains(runner.inputs[0], "make it green") ||
		!strings.Contains(runner.inputs[0], "GOAL_STATUS: met") {
		t.Fatalf("continuation was not the raw framed goal prompt: %q", runner.inputs)
	}
}

// TestGoalStallDetector proves the uncapped loop still can't run away: two
// consecutive no-progress iterations pause it (the goal clears, the loop ends).
func TestGoalStallDetector(t *testing.T) {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "GOAL_STATUS: unmet — keep going"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &scriptedGoalRunner{session: sess, replies: []string{"", ""}} // no progress
	c := New(Options{Runner: runner, Executor: exec, Label: "test"})
	c.SetGoal("impossible")

	if err := c.runGoalLoop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("calls = %d, want 2 (stall pauses after two no-progress iterations)", runner.calls)
	}
	if g, _ := c.Goal(); g != "" {
		t.Fatalf("goal should pause-clear after a stall, got %q", g)
	}
}

// TestGoalLoopCancelMidLoop checks a user cancel mid-run stops the loop and leaves
// the goal ARMED (never read as "unmet, keep going").
func TestGoalLoopCancelMidLoop(t *testing.T) {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "GOAL_STATUS: unmet — go"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &scriptedGoalRunner{session: sess, replies: []string{"progress\nGOAL_STATUS: unmet — more"}}
	runner.onRun = func(string) { cancel() } // simulate Esc during the run
	c := New(Options{Runner: runner, Executor: exec, Label: "test"})
	c.SetGoal("x")

	if err := c.runGoalLoop(ctx); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("calls = %d, want 1 (cancel stops further continuations)", runner.calls)
	}
	if g, _ := c.Goal(); g == "" {
		t.Fatal("a user cancel must leave the goal armed, not clear it")
	}
}

// TestParseGoalVerdictIgnoresStale is the anti-staleness guard: when the newest
// assistant message carries no verdict, an older verdict must NOT be inherited.
func TestParseGoalVerdictIgnoresStale(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, Content: "GOAL_STATUS: met — done"},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: "still working, no verdict here"},
	}
	if met, present, _ := parseGoalVerdict(msgs, 0); met || present {
		t.Fatal("newest assistant has no verdict → must be unmet, not the stale met")
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: "GOAL_STATUS: met — really done"})
	if met, present, _ := parseGoalVerdict(msgs, 0); !met || !present {
		t.Fatal("a fresh met in the newest message must be read")
	}
}

// TestStripGoalVerdict checks the display strip removes a trailing sentinel and
// leaves text without one untouched.
func TestStripGoalVerdict(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Here is the result.\n\nGOAL_STATUS: unmet — fix the test", "Here is the result."},
		{"done\nGOAL_STATUS: met — all green", "done"},
		{"plain answer with no sentinel", "plain answer with no sentinel"},
	}
	for _, tc := range cases {
		if got := StripGoalVerdict(tc.in); got != tc.want {
			t.Errorf("StripGoalVerdict(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGoalClearClears verifies SetGoal/ClearGoal toggle the state and that
// ClearGoal's Cancel() is a safe no-op when no turn is running.
func TestGoalClearClears(t *testing.T) {
	c := New(Options{Label: "test"})
	c.SetGoal("ship it")
	if g, _ := c.Goal(); g != "ship it" {
		t.Fatalf("SetGoal failed: %q", g)
	}
	c.ClearGoal()
	if g, n := c.Goal(); g != "" || n != 0 {
		t.Fatalf("ClearGoal failed: %q ×%d", g, n)
	}
}

// TestGoalAutoApproveIsolated proves the goal loop uses and clears its dedicated
// goalAutoApp flag.
func TestGoalAutoApproveIsolated(t *testing.T) {
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "GOAL_STATUS: unmet — go"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &scriptedGoalRunner{session: sess, replies: []string{"work\nGOAL_STATUS: met — done"}}
	c := New(Options{Runner: runner, Executor: exec, Label: "test"})
	var sawGoalAuto bool
	runner.onRun = func(string) {
		c.mu.Lock()
		sawGoalAuto = c.goalAutoApp
		c.mu.Unlock()
	}
	c.SetGoal("x")

	if err := c.runGoalLoop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sawGoalAuto {
		t.Fatal("goal continuation must set goalAutoApp")
	}
	c.mu.Lock()
	leftover := c.goalAutoApp
	c.mu.Unlock()
	if leftover {
		t.Fatal("goalAutoApp must be cleared when the loop ends")
	}
}
