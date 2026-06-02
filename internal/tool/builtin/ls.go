package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"roach-code/internal/tool"
)

func init() { tool.RegisterBuiltin(listDir{}) }

// listDir lists a directory. workDir, when non-empty, is the directory a
// relative path resolves against (see resolveIn).
type listDir struct{ workDir string }

func (listDir) Name() string { return "ls" }

func (listDir) Description() string {
	return "List the entries of a directory. Directories are shown with a trailing slash; files show their byte size."
}

func (listDir) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory path (default \".\")"}}}`)
}

func (listDir) ReadOnly() bool { return true }

func (l listDir) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	p := struct {
		Path string `json:"path"`
	}{Path: "."}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if p.Path == "" {
		p.Path = "."
	}
	p.Path = resolveIn(l.workDir, p.Path)

	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return "", fmt.Errorf("ls %s: %w", p.Path, err)
	}

	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		size := int64(-1)
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		fmt.Fprintf(&b, "%s\t%d\n", e.Name(), size)
	}
	if b.Len() == 0 {
		return "(empty directory)", nil
	}
	return b.String(), nil
}
