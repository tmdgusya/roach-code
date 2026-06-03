package cli

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"roach-code/internal/control"
)

// saveClipboardImageFn reads an image off the OS clipboard and returns its saved
// path; a var so tests can drive the paste→attach pipeline without a real
// clipboard (and PowerShell/osascript subprocess).
var saveClipboardImageFn = control.SaveClipboardImage

func pasteClipboardImage() tea.Cmd {
	return func() tea.Msg {
		path, err := saveClipboardImageFn()
		return clipboardImageMsg{path: path, err: err}
	}
}

func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		path, imageErr := saveClipboardImageFn()
		if imageErr == nil {
			return clipboardPasteMsg{path: path}
		}
		text, textErr := clipboard.ReadAll()
		if textErr == nil && text != "" {
			return clipboardPasteMsg{text: text}
		}
		if textErr != nil {
			return clipboardPasteMsg{err: fmt.Errorf("%v; text paste failed: %w", imageErr, textErr)}
		}
		return clipboardPasteMsg{err: imageErr}
	}
}

func (m *chatTUI) attachPastedImages(text string) bool {
	sources, ok := pastedImageSources(text)
	if !ok {
		return false
	}
	for _, src := range sources {
		path, err := savePastedImageSource(src)
		if err != nil {
			m.notice("paste image: " + err.Error())
			continue
		}
		m.insertImageRef(path)
	}
	return true
}

var markdownImageSourceRe = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

func pastedImageSources(text string) ([]string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, false
	}
	if isDataImage(trimmed) {
		return []string{trimmed}, true
	}
	if matches := markdownImageSourceRe.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
		rest := strings.TrimSpace(markdownImageSourceRe.ReplaceAllString(trimmed, ""))
		if rest == "" {
			sources := make([]string, 0, len(matches))
			for _, m := range matches {
				sources = append(sources, m[1])
			}
			return sources, true
		}
	}

	lines := nonEmptyPasteLines(trimmed)
	if len(lines) > 0 && allImageSources(lines) {
		return lines, true
	}
	fields := strings.Fields(trimmed)
	if len(fields) > 1 && allImageSources(fields) {
		return fields, true
	}
	return nil, false
}

func nonEmptyPasteLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func allImageSources(sources []string) bool {
	if len(sources) == 0 {
		return false
	}
	for _, src := range sources {
		if !looksLikeImageSource(src) {
			return false
		}
	}
	return true
}

func looksLikeImageSource(src string) bool {
	if isDataImage(strings.TrimSpace(src)) {
		return true
	}
	path, ok := pastedImagePath(src)
	if !ok {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func savePastedImageSource(src string) (string, error) {
	src = strings.TrimSpace(src)
	if isDataImage(src) {
		return control.SaveImageDataURL(src)
	}
	path, ok := pastedImagePath(src)
	if !ok {
		return "", fmt.Errorf("unsupported pasted image source")
	}
	return control.SaveImageFile(path)
}

func isDataImage(src string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "data:image/")
}

func pastedImagePath(src string) (string, bool) {
	src = strings.TrimSpace(src)
	src = strings.TrimPrefix(src, "@")
	quoted := (strings.HasPrefix(src, `"`) && strings.HasSuffix(src, `"`)) || (strings.HasPrefix(src, `'`) && strings.HasSuffix(src, `'`))
	src = strings.Trim(src, "\"'")
	if src == "" {
		return "", false
	}
	if !quoted && strings.ContainsAny(src, " \t\r\n") {
		return "", false
	}
	lower := strings.ToLower(src)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", false
	}
	if strings.HasPrefix(lower, "file://") {
		u, err := url.Parse(src)
		if err != nil || u.Path == "" {
			return "", false
		}
		src = u.Path
	}
	if strings.HasPrefix(src, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			src = filepath.Join(home, strings.TrimPrefix(src, "~/"))
		}
	}
	return filepath.Clean(src), true
}

// pastedFileRef turns a dragged/pasted non-image file path into an @reference so
// it attaches instead of landing as literal text (and, for a POSIX path, being
// misread as a slash command). Images are handled earlier; only path-shaped
// content (a separator) that points at a real file qualifies, so an ordinary
// pasted word is left alone.
func pastedFileRef(content string) (string, bool) {
	path, ok := pastedImagePath(content)
	if !ok || !strings.ContainsAny(path, `/\`) {
		return "", false
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return "@" + path, true
}
