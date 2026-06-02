package agent

import (
	"context"
	"math"
	"testing"

	"roach-code/internal/event"
	"roach-code/internal/provider"
	"roach-code/internal/tool"
)

// seqProvider replays a distinct chunk slice per Stream call, so a test can
// drive several turns with different usage through one agent. After the last
// preset turn it keeps replaying the final slice.
type seqProvider struct {
	name  string
	turns [][]provider.Chunk
	i     int
}

func (s *seqProvider) Name() string { return s.name }

func (s *seqProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	chunks := s.turns[len(s.turns)-1]
	if s.i < len(s.turns) {
		chunks = s.turns[s.i]
	}
	s.i++
	ch := make(chan provider.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// TestSessionCostAccumulates verifies the running spend estimate sums every
// turn's billable tokens at the model's rates — including the all-uncached turn
// where the provider leaves CacheMissTokens at 0 (the normaliser only derives
// miss when hit>0), which must still be priced as input rather than free.
func TestSessionCostAccumulates(t *testing.T) {
	price := &provider.Pricing{CacheHit: 0.1, Input: 1, Output: 2, Currency: "$"}
	prov := &seqProvider{name: "p", turns: [][]provider.Chunk{
		{ // turn 1: all uncached, miss field left unset
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}},
			{Type: provider.ChunkText, Text: "ok"},
			{Type: provider.ChunkDone},
		},
		{ // turn 2: partial cache hit, miss reported
			{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 2000, CompletionTokens: 300, TotalTokens: 2300, CacheHitTokens: 800, CacheMissTokens: 1200}},
			{Type: provider.ChunkText, Text: "ok"},
			{Type: provider.ChunkDone},
		},
	}}

	a := New(prov, tool.NewRegistry(), NewSession(""), Options{Pricing: price}, event.Discard)
	if cost, sym := a.SessionCost(); cost != 0 || sym != "$" {
		t.Fatalf("before any turn: got (%v, %q), want (0, \"$\")", cost, sym)
	}

	if err := a.Run(context.Background(), "one"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	// (0*0.1 + 1000*1 + 500*2) / 1e6 = 0.002
	if cost, _ := a.SessionCost(); math.Abs(cost-0.002) > 1e-9 {
		t.Fatalf("after turn 1: cost = %v, want 0.002", cost)
	}

	if err := a.Run(context.Background(), "two"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	// turn 2 adds (800*0.1 + 1200*1 + 300*2) / 1e6 = 0.00188 → total 0.00388
	cost, sym := a.SessionCost()
	if math.Abs(cost-0.00388) > 1e-9 {
		t.Fatalf("after turn 2: cost = %v, want 0.00388", cost)
	}
	if sym != "$" {
		t.Errorf("symbol = %q, want $", sym)
	}
}

// TestSessionCostNoPricing confirms an unpriced model yields no readout, so the
// status line omits the cost segment entirely rather than showing a bogus 0.
func TestSessionCostNoPricing(t *testing.T) {
	prov := &seqProvider{name: "p", turns: [][]provider.Chunk{{
		{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}},
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "x"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cost, sym := a.SessionCost(); cost != 0 || sym != "" {
		t.Errorf("unpriced model: got (%v, %q), want (0, \"\")", cost, sym)
	}
}
