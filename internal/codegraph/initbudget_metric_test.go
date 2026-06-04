package codegraph

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// --- CI-safe correctness tests (no exec, no timing, no background goroutine) ---

func TestShouldAutoIndex(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if ShouldAutoIndex(home) {
		t.Errorf("ShouldAutoIndex(home) = true, want false (home root must not be indexed)")
	}
	if ShouldAutoIndex("") {
		t.Errorf("ShouldAutoIndex(\"\") = true, want false")
	}
	proj := filepath.Join(home, "projects", "demo")
	if !ShouldAutoIndex(proj) {
		t.Errorf("ShouldAutoIndex(%q) = false, want true (real projects under home still index)", proj)
	}
}

// TestInitForBootSkipsHome verifies the home guard short-circuits *before* any
// exec, so it needs no binary and creates nothing in the real home directory.
func TestInitForBootSkipsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	status, err := InitForBoot(context.Background(), filepath.Join(t.TempDir(), "no-such-bin"), home, EnsureInitBudget)
	if err != nil {
		t.Fatalf("InitForBoot(home) err = %v", err)
	}
	if status != InitSkippedHome {
		t.Fatalf("InitForBoot(home) status = %d, want InitSkippedHome", status)
	}
}

// TestInitForBootReadyWhenInitialized covers the InitReady path with no binary:
// an already-initialised non-home project makes EnsureInit short-circuit, so
// InitForBoot returns InitReady instantly.
func TestInitForBootReadyWhenInitialized(t *testing.T) {
	root := t.TempDir() // under the OS temp dir, i.e. not the home root
	if err := os.Mkdir(filepath.Join(root, ".codegraph"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err := InitForBoot(context.Background(), filepath.Join(t.TempDir(), "no-such-bin"), root, EnsureInitBudget)
	if err != nil {
		t.Fatalf("InitForBoot(initialised) err = %v", err)
	}
	if status != InitReady {
		t.Fatalf("InitForBoot(initialised) status = %d, want InitReady", status)
	}
}

// --- Opt-in timing metric (gated out of CI) ---
//
// These build a stub binary and make wall-clock assertions, so they are gated on
// ROACH_CODEGRAPH_METRIC the same way the e2e test is gated: the default `go test
// ./...` (including the OS matrix and the -race leg) skips them, avoiding timing
// flakiness and a background-process/TempDir race on Windows, yet they still
// compile every build so they can't bit-rot. Reproduce the issue #4 before/after
// numbers with:
//
//	ROACH_CODEGRAPH_METRIC=1 go test ./internal/codegraph/ -run InitBudgetMetric -v -count=1

func requireMetricGate(t *testing.T) {
	t.Helper()
	if os.Getenv("ROACH_CODEGRAPH_METRIC") == "" {
		t.Skip("set ROACH_CODEGRAPH_METRIC=1 to run the codegraph init timing metric")
	}
}

// runGoBuild compiles the module in dir into out using the go toolchain.
func runGoBuild(t *testing.T, dir, out string) error {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Logf("go build output: %s", b)
		return err
	}
	return nil
}

// buildSleepStub compiles a tiny cross-platform "codegraph" stand-in that, when
// invoked as `<bin> init <root>`, sleeps for STUB_SLEEP_MS milliseconds and then
// creates .codegraph in its working directory — modelling a slow `codegraph init`
// (e.g. one pointed at a huge home tree) without needing the real binary. It is a
// real compiled .exe so it works on Windows too.
func buildSleepStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package main

import (
	"os"
	"strconv"
	"time"
)

func main() {
	ms := 3000
	if v := os.Getenv("STUB_SLEEP_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ms = n
		}
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	_ = os.MkdirAll(".codegraph", 0o755)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module stub\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "stub")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	if err := runGoBuild(t, dir, out); err != nil {
		t.Fatalf("build stub: %v", err)
	}
	return out
}

// TestInitBudgetMetric_Before measures the pre-fix behaviour: boot called
// codegraph.EnsureInit on the session ctx with no timeout, so a slow init blocked
// for its full duration — the multi-minute freeze from issue #4.
func TestInitBudgetMetric_Before(t *testing.T) {
	requireMetricGate(t)
	stub := buildSleepStub(t)
	t.Setenv("STUB_SLEEP_MS", "3000")

	root := t.TempDir()
	start := time.Now()
	_ = EnsureInit(context.Background(), stub, root) // unbounded — what boot did before
	elapsed := time.Since(start)

	t.Logf("BEFORE  EnsureInit(unbounded), slow init: blocked for %v", elapsed.Round(time.Millisecond))
	if elapsed < 2500*time.Millisecond {
		t.Fatalf("expected the unbounded init to block ~3s; got %v (stub did not run?)", elapsed)
	}
}

// TestInitBudgetMetric_After measures the fixed path: InitForBoot bounds the same
// slow init to the budget (detaching the rest to the background) instead of
// blocking for the full init duration.
func TestInitBudgetMetric_After(t *testing.T) {
	requireMetricGate(t)
	stub := buildSleepStub(t)
	t.Setenv("STUB_SLEEP_MS", "3000")
	const budget = 500 * time.Millisecond

	root := t.TempDir()
	start := time.Now()
	status, err := InitForBoot(context.Background(), stub, root, budget)
	bounded := time.Since(start)
	if err != nil {
		t.Fatalf("InitForBoot returned error: %v", err)
	}
	if status != InitPending {
		t.Fatalf("slow init should be InitPending, got status %d", status)
	}
	// Loose ceiling: prove it returned well before the full 3s init, tolerant of
	// slow/loaded runners — the point is "bounded", not a precise latency.
	if bounded >= 2500*time.Millisecond {
		t.Fatalf("InitForBoot blocked %v; should return well before the 3s init", bounded)
	}

	t.Logf("AFTER   InitForBoot(non-home, slow init): returned in %v (budget %v, status=pending, rest backgrounded)", bounded.Round(time.Millisecond), budget)

	// The InitPending path detached a background init still running the 3s stub
	// with its cwd inside root; wait for it to exit so t.TempDir cleanup does not
	// race the live process (Windows locks a process's working directory).
	time.Sleep(3500 * time.Millisecond)
}

// TestInitForBootReadyFast confirms the common case is untouched: a fast init
// returns InitReady well within the budget and creates .codegraph.
func TestInitForBootReadyFast(t *testing.T) {
	requireMetricGate(t)
	stub := buildSleepStub(t)
	t.Setenv("STUB_SLEEP_MS", "0")
	root := t.TempDir()
	status, err := InitForBoot(context.Background(), stub, root, EnsureInitBudget)
	if err != nil || status != InitReady {
		t.Fatalf("fast init: status=%d err=%v, want InitReady", status, err)
	}
	if fi, err := os.Stat(filepath.Join(root, ".codegraph")); err != nil || !fi.IsDir() {
		t.Fatalf(".codegraph not created by fast init: %v", err)
	}
}
