package workflow

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

// TestGojaAsyncBridge is the Phase 0 de-risking spike (docs/WORKFLOWS-PLAN.md §5).
// It proves the one genuinely uncertain mechanism the whole engine rests on:
// a JS `await Promise.all([...])` whose promises are resolved from *worker
// goroutines* (not the loop goroutine) returns ordered results and runs the
// work concurrently. If this passes, the agent() bridge in host.go is viable.
func TestGojaAsyncBridge(t *testing.T) {
	loop := eventloop.NewEventLoop()
	loop.Start()
	defer loop.Stop()

	var inFlight, maxInFlight int32
	resultCh := make(chan goja.Value, 1)
	errCh := make(chan error, 1)

	loop.RunOnLoop(func(vm *goja.Runtime) {
		// __agent(n): returns a Promise that resolves to "<n>!" after doing
		// "work" on a worker goroutine. Tracks peak concurrency so we can assert
		// the three calls actually overlapped.
		_ = vm.Set("__agent", func(call goja.FunctionCall) goja.Value {
			arg := call.Argument(0).String()
			p, resolve, reject := vm.NewPromise()

			go func() {
				cur := atomic.AddInt32(&inFlight, 1)
				for {
					m := atomic.LoadInt32(&maxInFlight)
					if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond) // simulate sub-agent latency
				atomic.AddInt32(&inFlight, -1)

				// Hop back onto the loop goroutine before touching the runtime.
				loop.RunOnLoop(func(vm *goja.Runtime) {
					if arg == "" {
						reject(vm.ToValue("empty arg"))
						return
					}
					resolve(vm.ToValue(arg + "!"))
				})
			}()

			return vm.ToValue(p)
		})

		script := `
			(async () => {
				const out = await Promise.all([1, 2, 3].map(n => __agent(String(n))));
				return out.join(",");
			})()
		`
		v, err := vm.RunString(script)
		if err != nil {
			errCh <- fmt.Errorf("RunString: %w", err)
			return
		}
		// The script's value is a Promise; resolve it via the runtime's then.
		p, ok := v.Export().(*goja.Promise)
		if !ok {
			errCh <- fmt.Errorf("expected a Promise, got %T", v.Export())
			return
		}
		// Poll the promise state on the loop until settled.
		var poll func()
		poll = func() {
			switch p.State() {
			case goja.PromiseStateFulfilled:
				resultCh <- p.Result()
			case goja.PromiseStateRejected:
				errCh <- fmt.Errorf("promise rejected: %v", p.Result())
			default:
				loop.RunOnLoop(func(*goja.Runtime) { poll() })
			}
		}
		poll()
	})

	select {
	case err := <-errCh:
		t.Fatalf("spike failed: %v", err)
	case v := <-resultCh:
		if got := v.String(); got != "1!,2!,3!" {
			t.Fatalf("ordered results wrong: got %q want %q", got, "1!,2!,3!")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out — async bridge did not settle")
	}

	if maxInFlight < 2 {
		t.Fatalf("agents did not run concurrently: peak in-flight = %d (want >= 2)", maxInFlight)
	}
}
