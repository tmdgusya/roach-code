package control

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFileRefLine(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := FileRefLine("  " + pdf + "  "); !ok || got != "@"+pdf {
		t.Fatalf("FileRefLine(existing) = %q, %v", got, ok)
	}
	if got, ok := FileRefLine(`"` + pdf + `"`); !ok || got != "@"+pdf {
		t.Fatalf("FileRefLine(quoted) = %q, %v", got, ok)
	}
	if _, ok := FileRefLine("/compact"); ok {
		t.Fatal("a slash command must not resolve as a file ref")
	}
	if _, ok := FileRefLine(dir); ok {
		t.Fatal("a directory must not resolve as a file ref")
	}
	if _, ok := FileRefLine(""); ok {
		t.Fatal("empty must not resolve as a file ref")
	}
}

func TestParseRefTokens(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"see @docs:doc://x and @src/main.go", []string{"docs:doc://x", "src/main.go"}},
		{"trailing @file.go.", []string{"file.go"}},
		{"dedup @a @a", []string{"a"}},
		{"no refs here", nil},
		{"email a@b.com keeps token", []string{"b.com"}},
	}
	for _, c := range cases {
		got := parseRefTokens(c.line)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRefTokens(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestClassifyRef(t *testing.T) {
	known := map[string]bool{"docs": true}
	files := map[string]bool{"src/main.go": true, "README.md": true, ".roach-code/attachments/clipboard-20260601-010203.000000.png": true}
	exists := func(p string) bool { return files[p] }

	cases := []struct {
		token   string
		wantOK  bool
		wantKnd refKind
	}{
		{"docs:doc://style", true, refResource}, // known server + uri
		{"src/main.go", true, refFile},          // existing file
		{"README.md", true, refFile},            // existing file
		{".roach-code/attachments/clipboard-20260601-010203.000000.png", true, refImage},
		{"ghost:issue://1", false, 0}, // unknown server, no such file
		{"missing.go", false, 0},      // nonexistent path → not a ref
		{"docs:", false, 0},           // empty uri → not a resource, no file
	}
	for _, c := range cases {
		r, ok := classifyRef(c.token, known, exists)
		if ok != c.wantOK {
			t.Errorf("classifyRef(%q) ok = %v, want %v", c.token, ok, c.wantOK)
			continue
		}
		if ok && r.kind != c.wantKnd {
			t.Errorf("classifyRef(%q) kind = %v, want %v", c.token, r.kind, c.wantKnd)
		}
	}
}

func TestResolveRefMessageIncludesImagePart(t *testing.T) {
	t.Chdir(t.TempDir())
	path, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	msg, errs := (&Controller{}).ResolveRefMessage(context.Background(), "describe @"+path)
	if len(errs) > 0 {
		t.Fatalf("errs = %v", errs)
	}
	if msg.Content != "describe [image1]" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("parts = %+v, want text + image", msg.Parts)
	}
	if msg.Parts[0].Type != "text" || msg.Parts[0].Text != msg.Content {
		t.Errorf("text part = %+v", msg.Parts[0])
	}
	if msg.Parts[1].Type != "image" || !strings.HasPrefix(msg.Parts[1].ImageURL, "data:image/png;base64,") {
		t.Errorf("image part = %+v", msg.Parts[1])
	}
}

func TestResolveRefMessageLabelsImageWithoutPathToolBait(t *testing.T) {
	t.Chdir(t.TempDir())
	path, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	msg, errs := (&Controller{}).ResolveRefMessage(context.Background(), "describe @"+path)
	if len(errs) > 0 {
		t.Fatalf("errs = %v", errs)
	}
	if strings.Contains(msg.Content, path) {
		t.Fatalf("content = %q, must not include attachment path %q", msg.Content, path)
	}
	if msg.Content != "describe [image1]" {
		t.Fatalf("content = %q, want image label", msg.Content)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("parts = %+v, want text + image", msg.Parts)
	}
	if msg.Parts[0].Text != msg.Content {
		t.Fatalf("text part = %q, want sanitized content %q", msg.Parts[0].Text, msg.Content)
	}
	if msg.Parts[1].Type != "image" || msg.Parts[1].ImageURL == "" {
		t.Fatalf("image part = %+v, want native image data", msg.Parts[1])
	}
}

func TestResolveRefMessageLabelsImageWhenMixedWithTextRefs(t *testing.T) {
	t.Chdir(t.TempDir())
	textPath := "note.txt"
	if err := os.WriteFile(textPath, []byte("context line"), 0o644); err != nil {
		t.Fatal(err)
	}
	imagePath, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	msg, errs := (&Controller{}).ResolveRefMessage(context.Background(), "compare @"+textPath+" with @"+imagePath)
	if len(errs) > 0 {
		t.Fatalf("errs = %v", errs)
	}
	if !strings.Contains(msg.Content, "Referenced context:") || !strings.Contains(msg.Content, "context line") {
		t.Fatalf("content = %q, want text ref context", msg.Content)
	}
	if strings.Contains(msg.Content, imagePath) {
		t.Fatalf("content = %q, must not include attachment path %q", msg.Content, imagePath)
	}
	if !strings.Contains(msg.Content, "compare @"+textPath+" with [image1]") {
		t.Fatalf("content = %q, want sanitized submitted line", msg.Content)
	}
	if len(msg.Parts) != 2 || msg.Parts[1].Type != "image" {
		t.Fatalf("parts = %+v, want text + image", msg.Parts)
	}
}

func TestResolveRefsDoesNotTellModelToUseOCRToolsForImageAttachments(t *testing.T) {
	t.Chdir(t.TempDir())
	path, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	block, errs := (&Controller{}).ResolveRefs(context.Background(), "describe @"+path)
	if len(errs) > 0 {
		t.Fatalf("errs = %v", errs)
	}
	for _, forbidden := range []string{"OCR", "MCP tool", "vision tool"} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("block = %q, must not steer model toward %q", block, forbidden)
		}
	}
}

func TestReadFileRef(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(textPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binPath, []byte{'a', 0x00, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	bigPath := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(bigPath, []byte(strings.Repeat("a", maxFileRefBytes+100)), 0o644); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Text file: content verbatim, not a directory.
	if got, isDir, err := readFileRef(textPath); err != nil || isDir || got != "line one\nline two\n" {
		t.Errorf("text file = (%q, %v, %v)", got, isDir, err)
	}

	// Binary file: noted, not dumped.
	if got, _, err := readFileRef(binPath); err != nil || !strings.Contains(got, "binary file") {
		t.Errorf("binary file = (%q, %v), want a binary note", got, err)
	}

	// Image file: identified as image-specific guidance, not generic binary.
	if got, _, err := readFileRef(imagePath); err != nil || !strings.Contains(got, "image file") {
		t.Errorf("image file = (%q, %v), want an image note", got, err)
	} else {
		for _, forbidden := range []string{"OCR", "MCP", "vision tool"} {
			if strings.Contains(got, forbidden) {
				t.Fatalf("image file note = %q, must not steer model toward %q", got, forbidden)
			}
		}
	}

	// Large file: truncated with a marker.
	if got, _, err := readFileRef(bigPath); err != nil || !strings.Contains(got, "truncated") {
		t.Errorf("big file should be truncated, got len=%d err=%v", len(got), err)
	}

	// Directory: recursive listing with relative paths including a trailing slash for subdirs.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, isDir, err := readFileRef(dir)
	if err != nil || !isDir {
		t.Fatalf("dir = (isDir=%v, err=%v)", isDir, err)
	}
	if !strings.Contains(got, "hello.txt") || !strings.Contains(got, "sub/") || !strings.Contains(got, "sub/nested.txt") {
		t.Errorf("dir listing = %q, want hello.txt, sub/, and sub/nested.txt", got)
	}

	// Missing path: error.
	if _, _, err := readFileRef(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing path should error")
	}
}
