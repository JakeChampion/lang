package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMutableScalarCaptureInterp pins SH-057 (#2850): the self-hosted
// tree-walking interpreter must implement MUTABLE SCALAR CAPTURES, matching the
// native reference.
//
// The language splits capture semantics deliberately. Scalars (i32 / bool /
// f64) are captured BY REFERENCE and a closure may write them —
// closures-as-counters is a supported feature. Reference-typed captures are
// read-only (E049), so a write-back cannot close a reference cycle; Fern has no
// cycle collector, the same model Roc uses. `Cell`'s own element-type
// restriction (E057 — scalars and strings only, for that same reason) lines up
// exactly with E049's, which is why the interpreter's cell is scalar-shaped.
//
// The bug survived a long time and was closed while half-open, which is what
// makes it worth pinning on both engines:
//
//   - the COMPILED path fixed it via box_mutated_scalar_captures (boxing the
//     captured scalar before the lambda lift), and #2850 was closed on that;
//   - `interp.fern` kept capturing by value, so a write inside the lambda
//     updated a private copy. The audit's repro returned 8 where the reference
//     says 49, and it still did at the time this test was written.
//
// It also hid easily: only a WRITE is affected, so any probe that reads the
// captured variable passes. Every case here therefore writes.
//
// Both engines are asserted against the native interpreter as the oracle rather
// than against stated values, so the test cannot drift from the language's
// definition of the semantics.
func TestSelfHostMutableScalarCaptureInterp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")

	for _, tc := range []struct {
		name string
		src  string
	}{
		// The audit's exact repro: a WRITE-ONLY capture. The lambda assigns `x`
		// without ever reading it, so a free-variable collector that only walks
		// an assignment's VALUE (not its TARGET) never sees `x` as free.
		{"write-only-capture", `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`},
		// The counter — read AND write. Captured by value this yields 0.
		{"counter", `function main(): i32 { var x = 0; var inc = function (): i32 { x = x + 1; return x; }; inc(); inc(); return x; }`},
		// TWO closures over the SAME variable must share one cell, not get one
		// each. This is the case a copy-in/copy-out fix would get wrong.
		{"two-closures-share", `function main(): i32 { var n: i32 = 0; var a: () => i32 = function (): i32 { n = n + 1; return n; }; var b: () => i32 = function (): i32 { n = n + 10; return n; }; a(); b(); return n; }`},
		// A boolean capture — the other scalar the language admits.
		{"bool-capture", `function main(): i32 { var b: boolean = false; var f: () => i32 = function (): i32 { b = true; return 0; }; f(); if (b) { return 7; } return 0; }`},

		// CONTROLS. A lambda-local `var x` shadows the outer one, so the outer
		// must NOT be celled or written; a read-only capture must be unaffected;
		// and reference captures (string / array) stay read-only per E049.
		{"inner-shadow-control", `function main(): i32 { var x: i32 = 1; var f: () => i32 = function (): i32 { var x: i32 = 5; x = x + 1; return x; }; var r = f(); return r + x; }`},
		{"read-only-capture-control", `function main(): i32 { var k: i32 = 40; var f: () => i32 = function (): i32 { return k + 2; }; return f(); }`},
		{"string-capture-control", `function main(): i32 { var s: string = "abcd"; var f: () => i32 = function (): i32 { return s.len(); }; return f(); }`},
		{"array-capture-control", `function main(): i32 { var xs: i32[] = [1,2,3]; var f: () => i32 = function (): i32 { return xs[2]; }; return f(); }`},
		// #6539 — the same binding ONE BLOCK DEEPER. The scan ran over
		// `fd.body` alone and never descended into a loop/if/match body, so a
		// `var g = <lambda>` inside a `while` was invisible and its capture kept
		// the construction-time snapshot.
		//
		// Note what writes: here the OUTER body assigns the capture, not the
		// lambda body (the #5394 clause rather than #2850's). Every case above
		// has the LAMBDA write, which is exactly why they all passed while this
		// shape did not — a probe whose lambda writes finds the binding by a
		// different route.
		{"nested-block-outer-write", `function main(): i32 { var fs: ((i32) => i32)[] = []; var i: i32 = 0; while (i < 3) { var g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`},
		// The control that pins the above as a REACH problem and not a broken
		// mechanism: identical binding, identical capture, one block shallower.
		// It was already correct, and must stay so.
		{"toplevel-outer-write-control", `function main(): i32 { var i: i32 = 0; var f: (i32) => i32 = ((x: i32) => x + i); i = 3; return f(0); }`},
		// Plain assignment and a loop counter: neither involves a lambda, so the
		// cell path must stay entirely out of the way.
		{"no-lambda-assign-control", `function main(): i32 { var x: i32 = 1; x = 41; return x + 1; }`},
		{"loop-counter-control", `function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 5) { t = t + i; i = i + 1; } return t; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			progPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(progPath, src, 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			// Oracle: the native interpreter defines the semantics.
			want := interpExit(t, interpBin, tc.src)

			got := runDriverExit(t, runner, interpDriver, src)
			if got != want {
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — mutable scalar captures are by-value again", tc.name, got, want)
			}
		})
	}
}

// runDriverExit pipes src into the driver and returns its exit code. The driver
// makes the program's result its own exit status, so a mismatch is the answer
// differing, not a crash.
func runDriverExit(t *testing.T, runner []string, bin string, src []byte) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = bytes.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatal("driver did not run")
	}
	return cmd.ProcessState.ExitCode()
}
