package agent

import (
	"context"
	"fmt"
	"strings"

	"roach-code/internal/event"
	"roach-code/internal/nilutil"
	"roach-code/internal/provider"
)

// Runner carries out one task turn. Both Agent (single model) and Coordinator
// (two-model) satisfy it, so the CLI stays agnostic to which is in use.
type Runner interface {
	Run(ctx context.Context, input string) error
	RunMessage(ctx context.Context, msg provider.Message) error
}

// DefaultPlannerPrompt steers the planner toward concise plans, not execution.
const DefaultPlannerPrompt = `You are the planner in a two-model coding agent.
Given a task, produce a concise, ordered plan for the executor model to carry out.
Do not write full implementations or call tools — outline the steps, which files
to touch, and the key decisions. Keep it short and actionable.`

// Coordinator runs two models in separate sessions to keep each one's prompt
// prefix cache-stable: a low-frequency planner proposes an approach, then the
// executor (a full tool-using Agent) carries it out. The sessions never mix, so
// neither model's prefix is disturbed by the other's turns.
type Coordinator struct {
	planner        provider.Provider
	plannerSess    *Session
	plannerPricing *provider.Pricing
	executor       *Agent
	temperature    float64
	sink           event.Sink
}

// NewCoordinator wires a planner provider (with its own session) to an executor.
// sink receives the planner's phase/text/usage events; the executor emits its
// own events to its own sink (the CLI wires the same sink into both). A nil
// sink is replaced with event.Discard.
func NewCoordinator(planner provider.Provider, plannerSession *Session, plannerPricing *provider.Pricing, executor *Agent, temperature float64, sink event.Sink) *Coordinator {
	if nilutil.IsNil(sink) {
		sink = event.Discard
	}
	return &Coordinator{
		planner:        planner,
		plannerSess:    plannerSession,
		plannerPricing: plannerPricing,
		executor:       executor,
		temperature:    temperature,
		sink:           sink,
	}
}

// Run plans with the planner model, then hands the plan to the executor.
func (c *Coordinator) Run(ctx context.Context, input string) error {
	return c.RunMessage(ctx, provider.Message{Role: provider.RoleUser, Content: input})
}

// RunMessage plans over the text fallback, then hands the original multimodal
// parts to the executor so pasted images still reach a vision-capable model.
func (c *Coordinator) RunMessage(ctx context.Context, msg provider.Message) error {
	c.sink.Emit(event.Event{Kind: event.TurnStarted})
	if messageHasImagePart(msg) {
		c.sink.Emit(event.Event{Kind: event.Phase, Text: c.executor.prov.Name() + " · executing"})
		return c.executor.RunMessage(ctx, msg)
	}
	c.sink.Emit(event.Event{Kind: event.Phase, Text: c.planner.Name() + " · planning"})
	plan, err := c.plan(ctx, msg.Content)
	if err != nil {
		return fmt.Errorf("planner: %w", err)
	}
	c.sink.Emit(event.Event{Kind: event.Phase, Text: c.executor.prov.Name() + " · executing"})
	msg.Content = formatHandoff(msg.Content, plan)
	ensureCoordinatorTextPart(&msg)
	return c.executor.RunMessage(ctx, msg)
}

func messageHasImagePart(msg provider.Message) bool {
	for _, p := range msg.Parts {
		if p.Type == "image" && p.ImageURL != "" {
			return true
		}
	}
	return false
}

func ensureCoordinatorTextPart(msg *provider.Message) {
	if msg.Content == "" || len(msg.Parts) == 0 {
		return
	}
	if msg.Parts[0].Type == "text" {
		msg.Parts[0].Text = msg.Content
		return
	}
	msg.Parts = append([]provider.ContentPart{{Type: "text", Text: msg.Content}}, msg.Parts...)
}

// plan streams a plan from the planner (no tools) and appends it to the planner
// session, so that session grows prepend-only and stays cache-friendly.
func (c *Coordinator) plan(ctx context.Context, input string) (string, error) {
	c.plannerSess.Add(provider.Message{Role: provider.RoleUser, Content: input})

	ch, err := c.planner.Stream(ctx, provider.Request{
		Messages:    c.plannerSess.Messages,
		Temperature: c.temperature,
	})
	if err != nil {
		return "", err
	}

	var text strings.Builder
	var usage *provider.Usage
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			text.WriteString(chunk.Text)
			c.sink.Emit(event.Event{Kind: event.Text, Text: chunk.Text})
		case provider.ChunkUsage:
			usage = chunk.Usage
		case provider.ChunkError:
			return "", chunk.Err
		}
	}
	// Closes the planner's raw text block (no markdown redraw) and prints its
	// usage line, mirroring the old Fprintln + printUsage tail.
	c.sink.Emit(event.Event{Kind: event.Usage, Usage: usage, Pricing: c.plannerPricing})

	plan := text.String()
	c.plannerSess.Add(provider.Message{Role: provider.RoleAssistant, Content: plan})
	return plan, nil
}

func formatHandoff(task, plan string) string {
	return fmt.Sprintf("Task: %s\n\nA planner proposed this approach:\n%s\n\nCarry it out, adapting as needed.", task, plan)
}
