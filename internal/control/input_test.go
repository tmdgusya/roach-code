package control

import (
	"context"
	"strings"
	"testing"

	"roach-code/internal/command"
	"roach-code/internal/provider"
)

type fakeTurnRunner struct {
	inputs []string
}

func (f *fakeTurnRunner) Run(ctx context.Context, input string) error {
	return f.RunMessage(ctx, provider.Message{Role: provider.RoleUser, Content: input})
}

func (f *fakeTurnRunner) RunMessage(_ context.Context, msg provider.Message) error {
	f.inputs = append(f.inputs, msg.Content)
	return nil
}

func TestCustomCommandLookup(t *testing.T) {
	c := New(Options{Commands: []command.Command{{Name: "review"}, {Name: "git:commit"}}})

	if _, ok := c.CustomCommand("/review the diff"); !ok {
		t.Error("review should be found")
	}
	if _, ok := c.CustomCommand("/git:commit"); !ok {
		t.Error("git:commit should be found")
	}
	if _, ok := c.CustomCommand("/missing"); ok {
		t.Error("missing should not be found")
	}
}

func TestComposeDrainsQueuedMemory(t *testing.T) {
	c := New(Options{}) // no executor/memory — QueueMemory still queues a turn-tail note

	c.QueueMemory("Saved memory \"rmb\": user's balance is in RMB")
	got := c.Compose("hello")
	if !strings.Contains(got, "<memory-update>") || !strings.Contains(got, "user's balance is in RMB") {
		t.Fatalf("queued memory should ride the turn: %q", got)
	}
	if !strings.HasSuffix(got, "hello") {
		t.Fatalf("user text should follow the memory block: %q", got)
	}
	if got2 := c.Compose("again"); got2 != "again" {
		t.Fatalf("pendingMemory should drain after one turn, got %q", got2)
	}
}

func TestRunTurnSendsInputVerbatim(t *testing.T) {
	runner := &fakeTurnRunner{}
	c := New(Options{Runner: runner})

	input := "实现 GitHub issue #2395：\n- 新增配置项\n- 补测试和文档"
	if err := c.runTurn(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != input {
		t.Fatalf("runTurn should compose verbatim, inputs=%q", runner.inputs)
	}
}

func TestRunTurnWithRawUsesResolvedInput(t *testing.T) {
	runner := &fakeTurnRunner{}
	c := New(Options{Runner: runner})

	resolved := "Referenced context:\n\n" + strings.Repeat("实现 重构 配置 测试 文档 多个文件\n", 20) + "\n\n解释 @foo.go"
	if err := c.runTurnWithRaw(context.Background(), resolved, "解释 @foo.go"); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != resolved {
		t.Fatalf("runner inputs = %q, want resolved input", runner.inputs)
	}
}
