package serve

import (
	"errors"
	"os"
	"strings"
	"testing"

	"roach-code/internal/event"
	"roach-code/internal/provider"
)

func TestToWire(t *testing.T) {
	t.Run("tool dispatch", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "bash", Args: `{"cmd":"ls"}`, ReadOnly: false}})
		if w.Kind != "tool_dispatch" || w.Tool == nil || w.Tool.Name != "bash" || w.Tool.Args != `{"cmd":"ls"}` {
			t.Errorf("dispatch = %+v / %+v", w, w.Tool)
		}
	})

	t.Run("usage with cost", func(t *testing.T) {
		w := toWire(event.Event{
			Kind:     event.Usage,
			ParentID: "task-1",
			Usage:    &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100},
			Pricing:  &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2},
		})
		if w.ParentID != "task-1" || w.Usage == nil || w.Usage.TotalTokens != 1200 || w.Usage.CostUSD <= 0 {
			t.Errorf("usage = %+v", w.Usage)
		}
	})

	t.Run("notice warn", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "truncated"})
		if w.Kind != "notice" || w.Level != "warn" || w.Text != "truncated" {
			t.Errorf("notice = %+v", w)
		}
	})

	t.Run("approval", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ApprovalRequest, ParentID: "task-1", Approval: event.Approval{ID: "3", Tool: "bash", Subject: "rm"}})
		if w.ParentID != "task-1" || w.Approval == nil || w.Approval.ParentID != "task-1" || w.Approval.ID != "3" || w.Approval.Tool != "bash" {
			t.Errorf("approval = %+v", w.Approval)
		}
	})

	t.Run("turn done error", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.TurnDone, Err: errors.New("boom")})
		if w.Kind != "turn_done" || w.Err != "boom" {
			t.Errorf("turn_done = %+v", w)
		}
	})
}

func TestServeHTMLUsesParentIDForSubagentNesting(t *testing.T) {
	b, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{
		"tool.parentId",
		"ensureChildren(parent).appendChild(card)",
		"isSubagentRootTool(tool.name)",
		"card.dataset.toolCount",
		"const reason=tool.err?' · '+(cancelled?'cancelled':tool.err):''",
		"recordSubagentUsage(e.parentId,e.usage.totalTokens||0)",
		"tokenText=tokens>0?' · '+fmtTok(tokens)+' tokens':''",
		"markSubagentApproval(e.parentId,e.approval)",
		"requested by '+escHtml(parent)",
		"function isCancelledError(err)",
		"(cancelled?'cancelled':tool.err)",
		"function jobsLabel(jobs)",
		"if(!running)turnInfo.textContent=jobsLabel(s.jobs)",
		".card-children",
		".card--child",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve UI must preserve subagent nesting hook %q", want)
		}
	}
}

func TestServeStatusUsesRetainedJobs(t *testing.T) {
	b, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "s.getCtrl().AllJobs()") {
		t.Fatal("/status should expose retained jobs, not only currently running jobs")
	}
}
