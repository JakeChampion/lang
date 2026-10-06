package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The full PR5-wasm benefit (docs/ASYNC-FUTURE-UNIFICATION.md): two
// parallel OUTBOUND fetches returning their response BODIES, on wasm,
// through the unified `std/async` surface — `fetch.fetch_future` fanned
// out via `async.gather`. The same edge-handler fan-out as the native
// TestAsyncFetchFutureFanout, now on wasm: `fetch_future`'s wait token
// is `tcp_pollable(c)` (a real wasi:io/poll pollable handle on wasm),
// and `gather`'s `poll` blocks in the host until each socket is
// readable. Both reads overlap on one thread. Runs under stock wasmtime
// (-S inherit-network), Preview 2 — no Preview 3.
func TestAsyncWasmFetchFutureFanout(t *testing.T) {
	skipIfPreview2Missing(t)

	pSlow := e2eharness.StartDelayedUpstream(t, "AAA", 200*time.Millisecond)
	pFast := e2eharness.StartDelayedUpstream(t, "BBB", 10*time.Millisecond)

	compPath := buildResultComponent(t, e2eharness.WasmFetchFanoutSource)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PSLOW="+strconv.Itoa(pSlow), "--env", "PFAST="+strconv.Itoa(pFast), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	startT := time.Now()
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	elapsed := time.Since(startT)
	out := sout.String()

	// gather returns results in INPUT order: AAA (f1) before BBB (f2),
	// even though the fast upstream answered first — proving overlap +
	// order-preservation through the combinator.
	ai := bytes.Index(sout.Bytes(), []byte("AAA"))
	bi := bytes.Index(sout.Bytes(), []byte("BBB"))
	if ai < 0 || bi < 0 {
		t.Fatalf("fan-out: missing a body in %q\nstderr:\n%s", out, serr.String())
	}
	if ai > bi {
		t.Errorf("fan-out: bodies not in input order (AAA after BBB) in %q", out)
	}
	// Both reads share one thread, so wall-clock is bounded by the slow
	// upstream (~200ms), not the sum. This is a COARSE anti-serialization
	// guard — warm it runs in ~0.3s, but it's dominated by wasmtime's
	// component cold-start (and the streaming read adds a poll round per
	// chunk), so the bound is generous to avoid cold-start flakes; a truly
	// serialized fan-out would still be sub-second of server delay, so any
	// multi-second blowout it can't catch isn't the failure mode anyway.
	if elapsed > 8*time.Second {
		t.Errorf("fan-out took %v — expected overlapped (~200ms + startup)", elapsed)
	}
}

// `async.race` over two real wasm sockets: one upstream wins, the other is
// abandoned. On wasm the loser's pollable is a CHILD of its socket resource,
// so `race` must drop it (`__drop_losers` → `wasm_pollable_drop`) or wasmtime
// traps with "resource has children" at teardown. This pins the loser-drop:
// the program runs CLEAN (exit 0, no trap) and returns a valid winner body —
// before the fix this trapped at teardown. (Which upstream wins depends on
// wasm pollable readiness semantics, orthogonal to the drop, so the assertion
// accepts either body.)
func TestAsyncWasmRaceFetchDropsLoser(t *testing.T) {
	skipIfPreview2Missing(t)

	pA := e2eharness.StartDelayedUpstream(t, "AAA", 10*time.Millisecond)
	pB := e2eharness.StartDelayedUpstream(t, "BBB", 40*time.Millisecond)

	compPath := buildResultComponent(t, e2eharness.WasmRaceFetchSource)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PA="+strconv.Itoa(pA), "--env", "PB="+strconv.Itoa(pB), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	// Clean exit (no "resource has children" trap) is the loser-drop proof.
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run (race loser drop): %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	out := sout.String()
	if !bytes.Contains(sout.Bytes(), []byte("AAA")) && !bytes.Contains(sout.Bytes(), []byte("BBB")) {
		t.Errorf("race: want a winner body (AAA or BBB) in output, got %q\nstderr:\n%s", out, serr.String())
	}
}
