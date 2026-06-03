package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestEmptyPasteAttachesClipboardImage proves the Warp Ctrl+V routing fix end to
// end: an idle empty bracketed paste (the tell-tale of an image the terminal
// can't deliver through the PTY as text) probes the OS clipboard for an image
// and, on success, drops an [image #N] token into the composer — no key event
// required, so it works even when the terminal swallows Ctrl+V for its own paste.
func TestEmptyPasteAttachesClipboardImage(t *testing.T) {
	defer func(fn func() (string, error)) { saveClipboardImageFn = fn }(saveClipboardImageFn)
	const fakePath = ".roach-code/attachments/clipboard-20260601-010203.000001.png"
	saveClipboardImageFn = func() (string, error) { return fakePath, nil }

	m := newTestChatTUI()
	next, cmd := m.update(tea.PasteMsg{Content: ""})
	if cmd == nil {
		t.Fatal("an empty idle paste must return a clipboard-image probe command")
	}
	// Drive the probe to its message, then feed it back the way the runtime would.
	var img tea.Msg
	for _, msg := range runCmdMsgs(cmd) {
		if _, ok := msg.(clipboardImageMsg); ok {
			img = msg
		}
	}
	if img == nil {
		t.Fatal("empty paste did not route to a clipboard image probe")
	}
	final, _ := next.(chatTUI).update(img)
	fm := final.(chatTUI)
	if got := fm.input.Value(); !strings.Contains(got, "[image #1]") {
		t.Fatalf("composer should hold the [image #1] token, got %q", got)
	}
	if len(fm.pastedBlocks) != 1 || fm.pastedBlocks[0].text != "@"+fakePath || !fm.pastedBlocks[0].image {
		t.Fatalf("pasted block should map [image #1] -> @%s (image), got %+v", fakePath, fm.pastedBlocks)
	}
}

// runCmdMsgs executes a command and any commands it batches, returning every
// non-batch message produced (so a test can follow a finalize(probe) return).
func runCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			out = append(out, runCmdMsgs(c)...)
		}
		return out
	}
	if msg != nil {
		out = append(out, msg)
	}
	return out
}

func TestExpandPastedBlocksImage(t *testing.T) {
	m := &chatTUI{pastedBlocks: []pastedBlock{
		{label: "[image #1]", text: "@.roach-code/attachments/clipboard-20260601-010203.000001.png", image: true},
		{label: "[Pasted text #2 · 3 lines]", text: "a\nb\nc"},
	}}
	got := m.expandPastedBlocks("look at [image #1] and [Pasted text #2 · 3 lines]")
	want := "look at @.roach-code/attachments/clipboard-20260601-010203.000001.png and " +
		renderFoldedPasteBlock(m.pastedBlocks[1])
	if got != want {
		t.Fatalf("expandPastedBlocks = %q, want %q", got, want)
	}
	if displayLineForImageRefs(got) != "look at [image1] and "+renderFoldedPasteBlock(m.pastedBlocks[1]) {
		t.Fatalf("image ref should collapse to a label in the bubble: %q", displayLineForImageRefs(got))
	}
}

func TestDisplayLineForImageRefs(t *testing.T) {
	got := displayLineForImageRefs("describe @.roach-code/attachments/clipboard-20260601-010203.000001.png @.roach-code/attachments/clipboard-20260601-010204.000002-000002.jpg")
	want := "describe [image1] [image2]"
	if got != want {
		t.Fatalf("displayLineForImageRefs = %q, want %q", got, want)
	}
}

func TestPastedFileRef(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := pastedFileRef(pdf); !ok || got != "@"+filepath.Clean(pdf) {
		t.Fatalf("pastedFileRef(existing pdf) = %q, %v", got, ok)
	}
	if got, ok := pastedFileRef(`"` + pdf + `"`); !ok || got != "@"+filepath.Clean(pdf) {
		t.Fatalf("pastedFileRef(quoted pdf) = %q, %v", got, ok)
	}
	if _, ok := pastedFileRef("just-a-word"); ok {
		t.Fatal("a bare word with no separator must not be a file ref")
	}
	if _, ok := pastedFileRef(filepath.Join(dir, "missing.pdf")); ok {
		t.Fatal("a non-existent path must not be a file ref")
	}
	if _, ok := pastedFileRef(dir); ok {
		t.Fatal("a directory must not be a file ref")
	}
}

func TestPastedImageSources(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		ok   bool
	}{
		{
			name: "data URL",
			text: "data:image/png;base64,aaa",
			want: []string{"data:image/png;base64,aaa"},
			ok:   true,
		},
		{
			name: "markdown images",
			text: "![a](/tmp/a.png)\n![b](file:///tmp/b.jpg)",
			want: []string{"/tmp/a.png", "file:///tmp/b.jpg"},
			ok:   true,
		},
		{
			name: "plain text",
			text: "hello /tmp/a.png",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pastedImageSources(c.text)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("sources = %v, want %v", got, c.want)
			}
		})
	}
}
