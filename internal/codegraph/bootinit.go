package codegraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnsureInitBudget bounds how long boot will wait for `codegraph init` before it
// stops blocking the UI and finishes the init off the boot path. A bare init only
// creates .codegraph/ and is normally ~100ms, so a multi-second wait means a
// pathological tree (e.g. a whole home directory): blocking on it freezes startup
// and every /model switch for minutes (issue #4). Past the budget we defer.
const EnsureInitBudget = 2 * time.Second

// InitStatus reports how InitForBoot resolved, so boot can decide whether to
// start the eager serve this session and what to tell the user.
type InitStatus int

const (
	// InitReady: .codegraph/ is present (or was just created) — start serve now.
	InitReady InitStatus = iota
	// InitSkippedHome: cwd is the user's home directory — codegraph is disabled
	// for this session (indexing an entire home tree is never what's wanted).
	InitSkippedHome
	// InitPending: init exceeded the budget — it is finishing in the background,
	// so symbol tools come online next session rather than blocking this one.
	InitPending
	// InitFailed: init returned a real error (not a timeout). err is non-nil.
	InitFailed
)

// ShouldAutoIndex reports whether codegraph should auto-init/index for cwd.
//
// It returns false for the user's home directory *itself*: a code-intelligence
// indexer pointed at an entire home tree (AppData, Documents, caches, …) does not
// finish quickly and thrashes the machine — the exact trigger behind issue #4,
// where the reporter ran roach-code in `~`. Real projects living *under* home
// still index normally; only the home root is refused. When the home directory
// can't be determined, it errs toward indexing (returns true) so nothing is lost
// on platforms where this guard can't apply.
func ShouldAutoIndex(cwd string) bool {
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return true
	}
	return !sameDir(cwd, home)
}

// sameDir reports whether two paths point at the same directory, tolerating
// relative inputs and case-insensitive filesystems (Windows, default macOS).
func sameDir(a, b string) bool {
	ax, err1 := filepath.Abs(a)
	bx, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	ax = filepath.Clean(ax)
	bx = filepath.Clean(bx)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(ax, bx)
	}
	return ax == bx
}

// InitForBoot prepares codegraph for a boot/model-switch without ever blocking
// the caller for more than budget. It applies the home-directory guard, then runs
// EnsureInit under a deadline; if init outlasts the budget it is detached to
// finish in the background (a partial .codegraph/ is fine — serve re-syncs an
// existing index on connect). The returned status tells boot whether to start the
// eager serve now. budget <= 0 uses EnsureInitBudget.
func InitForBoot(ctx context.Context, bin, cwd string, budget time.Duration) (InitStatus, error) {
	if !ShouldAutoIndex(cwd) {
		return InitSkippedHome, nil
	}
	if budget <= 0 {
		budget = EnsureInitBudget
	}
	initCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	err := EnsureInit(initCtx, bin, cwd)
	switch {
	case err == nil:
		return InitReady, nil
	case errors.Is(initCtx.Err(), context.DeadlineExceeded):
		// Init is slow for this tree. Don't hold up the UI: finish it off the boot
		// path on the session ctx so the index is ready next session.
		go func() { _ = EnsureInit(context.WithoutCancel(ctx), bin, cwd) }()
		return InitPending, nil
	default:
		return InitFailed, err
	}
}
