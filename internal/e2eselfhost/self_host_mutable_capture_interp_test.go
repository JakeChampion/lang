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
		// Plain assignment and a loop counter: neither involves a lambda, so the
		// cell path must stay entirely out of the way.
		{"no-lambda-assign-control", `function main(): i32 { var x: i32 = 1; x = 41; return x + 1; }`},
		{"loop-counter-control", `function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 5) { t = t + i; i = i + 1; } return t; }`},

		// The OUTER-write clause (#5394), which this engine did not implement at
		// all until #6578 — every case above has the LAMBDA do the writing, which
		// is why twelve green cases coexisted with a silent wrong answer. Here the
		// lambda only READS and the enclosing body reassigns afterwards, so a
		// by-value snapshot freezes the pre-assignment value: the first case
		// answered 0 against the oracle's 3.
		{"outer-write-toplevel", `function main(): i32 { var i: i32 = 0; var f: (i32) => i32 = ((x: i32) => x + i); i = 3; return f(0); }`},
		{"outer-write-in-loop", `function main(): i32 { var fs: ((i32) => i32)[] = []; var i: i32 = 0; while (i < 3) { var g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`},
		// The lambda sits in an OPERAND of the init rather than being it — here a
		// call argument. Same clause, a position the statement-level scan has to
		// look inside.
		//
		// The compiled-path suite's `struct-literal-field` twin joins them now
		// that this engine can CALL a closure held in a struct field (#6596). It
		// used to die before reaching the capture clause at all, on
		// "undefined method `f` on `C`" — a separate gap that made the shape
		// untestable here rather than a capture failure.
		{"outer-write-call-argument", `function take(f: (i32) => i32): i32 { return f(0); } function main(): i32 { var i: i32 = 0; var fs: ((i32) => i32)[] = []; fs = fs.append(((x: i32) => x + i)); i = 4; return take(fs[0]); }`},
		{"outer-write-struct-field", `struct C { f: (i32) => i32, n: i32 } function main(): i32 { var cs: C[] = []; var i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`},

		// The guard on that clause, and the reason it is keyed on REASSIGNMENT
		// rather than on being captured at all. `n` is declared fresh each
		// iteration and never reassigned, so each closure must keep its own
		// value: the oracle answers 0+1+2, not three copies of the last one.
		// Celling every read capture would pass every case above and fail this.
		{"per-iteration-capture-control", `function main(): i32 { var t: i32 = 0; var fs: (() => i32)[] = []; var k: i32 = 0; while (k < 3) { var n: i32 = k; fs = fs.append(() => n); k = k + 1; } for f in fs { t = t + f(); } return t; }`},

		// Both clauses again, in the statement positions the per-statement scan
		// did not look at. It matched FOUR statement shapes — `var`, assignment,
		// `return`, expression — and took one field from each, so a lambda in an
		// `if` / `while` CONDITION, a `for`'s ITERATED expression or a `match`
		// SCRUTINEE was invisible: the captured name was never celled and the
		// closure got a private copy. Every one of these answered 1 against the
		// oracle's 9.
		//
		// A statement's own expressions are a fact about the Stmt union, so the
		// scan reads them from astwalk (fold_stmt_own) rather than from a
		// hand-written match that can be short by a variant.
		{"outer-write-for-iter", `function main(): i32 { var n: i32 = 1; var total: i32 = 0; for f in [function (): i32 { return n; }] { n = 9; total = total + f(); } return total; }`},
		{"lambda-write-for-iter", `function main(): i32 { var n: i32 = 1; var total: i32 = 0; for f in [function (): i32 { n = 9; return 0; }] { total = f(); } return n; }`},
		{"lambda-write-if-cond", `function id(x: i32): i32 { return x; } function main(): i32 { var n: i32 = 1; if (id((function (): i32 { n = 9; return 1; })()) == 1) { return n; } return 0; }`},
		{"lambda-write-while-cond", `function id(x: i32): i32 { return x; } function main(): i32 { var n: i32 = 1; var i: i32 = 0; while (i < 1 && id((function (): i32 { n = 9; return 1; })()) == 1) { i = i + 1; } return n; }`},
		{"lambda-write-match-scrutinee", `enum W { One(i32) } function main(): i32 { var n: i32 = 1; match (W.One((function (): i32 { n = 9; return 1; })())) { W.One(_) => { return n; }, } return 0; }`},
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
