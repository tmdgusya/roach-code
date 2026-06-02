// update_models.go — surgical, in-place rewriting of provider model lists.
//
// `roach-code models refresh` re-fetches a provider's GET /models list and
// writes it back to the user's TOML. We deliberately do NOT round-trip through
// RenderTOML for this: that would reformat the whole file (normalizing the
// user's comments and section layout) and historically dropped fields the
// renderer didn't know about. Instead we edit only the `models`/`default` lines
// of the targeted [[providers]] blocks, leaving every other byte — comments,
// ordering, auth, EOL style — exactly as the user wrote it.
package config

import (
	"fmt"
	"sort"
	"strings"
)

// ModelsUpdate is the refreshed model list (and chosen default) for one
// provider. Default may be empty to leave any existing default line untouched.
type ModelsUpdate struct {
	Models  []string
	Default string
}

// RewriteProviderModels rewrites, inside TOML source `src`, only the
// `models`/`default` lines of each [[providers]] block whose name is a key in
// updates. A block declared with a singular `model = "x"` is upgraded to a
// `models = [...]` list (plus a `default`). Everything outside the touched
// lines is preserved verbatim, including the file's CRLF/LF style. Names in
// updates with no matching block are returned in `missing` (sorted).
func RewriteProviderModels(src string, updates map[string]ModelsUpdate) (out string, missing []string) {
	crlf := strings.Contains(src, "\r\n")
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	type block struct {
		name       string
		start, end int // [start,end); start is the "[[providers]]" header line
	}
	var blocks []block
	cur := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "[[providers]]" {
			if cur >= 0 {
				blocks[cur].end = i
			}
			blocks = append(blocks, block{start: i, end: len(lines)})
			cur = len(blocks) - 1
			continue
		}
		if cur >= 0 && strings.HasPrefix(t, "[") { // any later table header ends the block
			blocks[cur].end = i
			cur = -1
		}
		if cur >= 0 {
			if name, ok := scanTOMLString(ln, "name"); ok {
				blocks[cur].name = name
			}
		}
	}

	seen := make(map[string]bool, len(updates))
	result := make([]string, 0, len(lines)+len(updates))
	pos := 0
	for _, blk := range blocks {
		for ; pos < blk.start; pos++ {
			result = append(result, lines[pos])
		}
		if up, ok := updates[blk.name]; ok && blk.name != "" {
			seen[blk.name] = true
			result = append(result, transformProviderBlock(lines[blk.start:blk.end], up)...)
		} else {
			result = append(result, lines[blk.start:blk.end]...)
		}
		pos = blk.end
	}
	for ; pos < len(lines); pos++ {
		result = append(result, lines[pos])
	}

	for name := range updates {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	out = strings.Join(result, "\n")
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, missing
}

// transformProviderBlock returns the block with its model/models line replaced
// by the refreshed list and its default line updated. A singular `model` line
// becomes a `models` list. If no default line exists one is inserted right after
// the models line.
func transformProviderBlock(block []string, up ModelsUpdate) []string {
	modelsLine := "models      = " + renderStringArray(up.Models)
	defaultLine := ""
	if up.Default != "" {
		defaultLine = fmt.Sprintf("default     = %q", up.Default)
	}

	out := make([]string, 0, len(block)+1)
	modelsIdx := -1
	wroteDefault := false
	for _, ln := range block {
		key, ok := tomlKey(ln)
		switch {
		case ok && (key == "models" || key == "model"):
			if modelsIdx < 0 {
				out = append(out, modelsLine)
				modelsIdx = len(out) - 1
			} // drop any duplicate model/models lines
		case ok && key == "default":
			if defaultLine != "" {
				out = append(out, defaultLine)
				wroteDefault = true
			} else {
				out = append(out, ln)
			}
		default:
			out = append(out, ln)
		}
	}

	if modelsIdx < 0 { // block had no model line at all — append at the end
		out = append(out, modelsLine)
		modelsIdx = len(out) - 1
	}
	if defaultLine != "" && !wroteDefault {
		res := make([]string, 0, len(out)+1)
		res = append(res, out[:modelsIdx+1]...)
		res = append(res, defaultLine)
		res = append(res, out[modelsIdx+1:]...)
		out = res
	}
	return out
}

// tomlKey returns the (trimmed) key of a `key = value` line, or ok=false for
// blanks, comments, and non-assignment lines.
func tomlKey(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	k, _, ok := strings.Cut(s, "=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(k), true
}

// scanTOMLString returns the string value of `key = "..."` on this line.
func scanTOMLString(line, key string) (string, bool) {
	k, ok := tomlKey(line)
	if !ok || k != key {
		return "", false
	}
	_, v, _ := strings.Cut(line, "=")
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' {
		if i := strings.IndexByte(v[1:], '"'); i >= 0 {
			return v[1 : 1+i], true
		}
	}
	return "", false
}
