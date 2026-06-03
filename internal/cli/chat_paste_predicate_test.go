package cli

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestIsDataImage proves the data-URL image sniff is case- and
// whitespace-insensitive on the "data:image/" prefix and rejects everything else.
func TestIsDataImage(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"png data url", "data:image/png;base64,xx", true},
		{"uppercase with leading space", "  DATA:IMAGE/JPEG;base64,yy", true},
		{"text data url", "data:text/plain;base64,zz", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDataImage(c.src); got != c.want {
				t.Fatalf("isDataImage(%q) = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

// TestPastedImagePath proves the path normalizer strips a leading @, honours the
// quoting gate around whitespace, rejects http(s) URLs, unwraps file:// URLs, and
// cleans the result with filepath.Clean (so the expectation is OS-normalized).
func TestPastedImagePath(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantOK  bool
		wantRaw string // pre-Clean path; compared after filepath.Clean
	}{
		{"plain relative", "a.png", true, "a.png"},
		{"at-prefixed absolute", "@/tmp/a.png", true, "/tmp/a.png"},
		{"quoted path with space", `"/with space/a.png"`, true, "/with space/a.png"},
		{"unquoted path with space", "/with space/a.png", false, ""},
		{"http url", "http://x/a.png", false, ""},
		{"https url", "https://x/a.png", false, ""},
		{"file url", "file:///tmp/a.png", true, "/tmp/a.png"},
		{"empty", "", false, ""},
		{"only at", "@", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pastedImagePath(c.src)
			if ok != c.wantOK {
				t.Fatalf("pastedImagePath(%q) ok = %v, want %v", c.src, ok, c.wantOK)
			}
			if !ok {
				return
			}
			want := filepath.Clean(c.wantRaw)
			if got != want {
				t.Fatalf("pastedImagePath(%q) = %q, want %q", c.src, got, want)
			}
		})
	}
}

// TestLooksLikeImageSource proves the predicate accepts data-image URLs and any
// path whose (case-insensitive) extension is a known raster image type, and
// rejects non-image extensions and http(s) URLs.
func TestLooksLikeImageSource(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"data image", "data:image/png;base64,xx", true},
		{"uppercase png ext", "x.PNG", true},
		{"jpeg ext", "a.jpeg", true},
		{"gif ext", "a.gif", true},
		{"webp ext", "a.webp", true},
		{"text ext", "x.txt", false},
		{"http png url", "http://x/a.png", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeImageSource(c.src); got != c.want {
				t.Fatalf("looksLikeImageSource(%q) = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

// TestNonEmptyPasteLines proves CRLF is normalized to LF, blank lines are
// dropped, and surviving lines are trimmed.
func TestNonEmptyPasteLines(t *testing.T) {
	got := nonEmptyPasteLines("a\r\n\n b \nc")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nonEmptyPasteLines = %v, want %v", got, want)
	}
}

// TestAllImageSources proves an empty slice is false, an all-image slice is true,
// and a single non-image entry drops the whole slice to false.
func TestAllImageSources(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		want    bool
	}{
		{"empty", nil, false},
		{"all images", []string{"a.png", "b.jpg"}, true},
		{"one non-image", []string{"a.png", "notes.txt"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allImageSources(c.sources); got != c.want {
				t.Fatalf("allImageSources(%v) = %v, want %v", c.sources, got, c.want)
			}
		})
	}
}

// TestPastedImageSourcesPredicate exercises the branch structure of
// pastedImageSources: the newline branch, the whitespace-fields branch, the
// single-token len>1 guard, the markdown branch's empty-remainder requirement,
// and the empty/whitespace short-circuits. (Named to avoid colliding with the
// existing TestPastedImageSources in chat_attachment_test.go.)
func TestPastedImageSourcesPredicate(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		ok   bool
	}{
		{"newline branch", "a.png\nb.jpg", []string{"a.png", "b.jpg"}, true},
		{"fields branch", "a.png b.jpg", []string{"a.png", "b.jpg"}, true},
		// A lone image token still matches via the single-line branch
		// (len(lines)>0 && allImageSources), so it returns the one source. The
		// len>1 guard the plan referenced lives only on the strings.Fields
		// fallback, which is never reached for a single line.
		{"single token via line branch", "a.png", []string{"a.png"}, true},
		{"markdown with leftover text", "![a](/x.png) leftover text", nil, false},
		{"empty", "", nil, false},
		{"whitespace only", "   ", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pastedImageSources(c.text)
			if ok != c.ok {
				t.Fatalf("pastedImageSources(%q) ok = %v, want %v", c.text, ok, c.ok)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("pastedImageSources(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
