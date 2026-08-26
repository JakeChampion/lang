package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTrmcIRX86_64 pins the #4352 slice-1 TRMC port: the canonical
// single-hole modulo-cons shape (`return Cons(g(h), self(t))` under a
// single-match-on-enum-param body) lowers as a hole-passing loop — each
// iteration builds the node with a dummy tail (normal op_struct_make, so
// layout/rc parity is automatic), links it into the previous hole via a plain
// op_struct_set at the held node's compile-time tail index, rebinds the params,
// and continues. O(1) stack: a 300k-element inc_all SIGSEGVs pre-port (one frame
// per element) and completes post-port.
//
// Recursive arms may build DIFFERENT variants (#5334) as long as they agree on
// the hole's PAYLOAD POSITION, which is what makes the link's field index a
// single constant. Since #5334 the detector also admits `when` guards, a `_`
// arm, statements before the match and before an arm's tail, a tail `if`/`else`,
// a bare tail self-call, and a hole that is not the last payload. Tree-shaped
// bodies (two self-calls) and ctors whose holes sit at different positions keep
// the plain recursion — the detector bails, which countSelfCalls witnesses.
func TestSelfHostTrmcIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) string {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (1 = wrong value; 99+ = over-release; 139 = stack overflow → TRMC didn't fire)", name, code, want)
		}
		return string(asm)
	}

	// VALUE + BALANCE: the native trmcMapSrc shape — inc_all over build(50),
	// sum(1..50) = 1275, detector 0.
	run(t, `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = inc_all(build(50)); if (sum(ys) != 1275) { return 1; } return __rc_underflow(); }`,
		"trmc-value", 0)

	// O(1) STACK: 300k elements — pre-port this SIGSEGVs (verified: exit 139 on
	// the pre-port driver), post-port it completes with the right sum.
	run(t, `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = inc_all(build(300000)); if (sum(ys) != 600000) { return 1; } return __rc_underflow(); }`,
		"trmc-deep-stack", 0)

	// STRING-HEAD payloads ride the same rewrite (width-0 pointer fields).
	run(t, `enum SList { SCons(string, SList), SNil }
function tag_all(xs: SList): SList {
    match (xs) {
        SCons(h, t) => { return SCons(h + "!", tag_all(t)); },
        SNil => { return SNil; },
    }
}
function len_all(l: SList): i32 { var acc: i32 = 0; var cur: SList = l; var go: boolean = true; while (go) { match (cur) { SCons(h, t) => { acc = acc + h.len(); cur = t; }, SNil => { go = false; } } } return acc; }
function main(): i32 { var xs: SList = SCons("ab", SCons("cde", SNil)); var ys: SList = tag_all(xs); if (len_all(ys) != 7) { return 1; } return __rc_underflow(); }`,
		"trmc-string-head", 0)

	// TREE-SHAPED (two self-calls) keeps the plain recursion — detection bails,
	// values stay correct via the normal path.
	run(t, `enum Tree { Node(Tree, Tree), Leaf(i32) }
function deep(t: Tree): Tree {
    match (t) {
        Node(l, r) => { return Node(deep(l), deep(r)); },
        Leaf(v) => { return Leaf(v + 1); },
    }
}
function main(): i32 { var t: Tree = Node(Leaf(1), Node(Leaf(2), Leaf(3))); var u: Tree = deep(t); match (u) { Node(a, b) => { match (a) { Leaf(x) => { return x; }, Node(c, d) => { return 90; } } }, Leaf(y) => { return 91; } } return 92; }`,
		"trmc-tree-not-rewritten", 2)

	// MIXED-VARIANT recursive arms (#5334): each arm rebuilds its OWN ctor, so
	// `score` (which signs Cons and Neg oppositely) separates a correct rewrite
	// from one that stamped every node with the first arm's variant — the latter
	// scores 60, not 6. `__rc_underflow()` covers the consuming traversal, which
	// now runs once per recursive arm.
	run(t, mixedCtorProg(3, 6), "trmc-mixed-ctor-value", 0)

	// …and the O(1)-stack discriminator on the same shape: 200k cells, one frame
	// per cell without the rewrite.
	run(t, mixedCtorProg(100000, 200000), "trmc-mixed-ctor-deep", 0)

	// The gate is on the arity of the ctors BUILT, not of the variants matched: a
	// one-payload `B` cell can be walked (and shallow-freed by the consuming
	// traversal) while the node built over it is a two-payload `A`.
	run(t, mixedScrutArityProg(), "trmc-mixed-ctor-narrow-scrutinee", 0)

	// EXCLUDED — recursive ctors whose holes sit at different payload POSITIONS.
	// The hole is filled by an op_struct_set at a compile-time field index, so the
	// tail cannot sit at index 1 in one iteration and index 0 in the next; the
	// detector bails and the self-calls stay in the asm. (Differing ARITY is fine
	// now, as long as the hole index agrees — that is what trmc_hole_index pins.)
	asm := run(t, `enum List { Cons(i32, List), Wrap(List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, step(t)); },
        Wrap(t) => { return Wrap(step(t)); },
        Nil => { return Nil; },
    }
}
function score(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Wrap(t) => { acc = acc + 100; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var xs: List = Cons(1, Wrap(Cons(2, Nil))); if (score(step(xs)) != 105) { return 1; } return __rc_underflow(); }`,
		"trmc-mixed-hole-pos-not-rewritten", 0)
	if n := countSelfCalls(asm, "step"); n < 2 {
		t.Errorf("trmc-mixed-hole-pos-not-rewritten: %d call sites to step in the asm, want more than main's one (TRMC must not fire when the hole position differs between arms)", n)
	}

	// GUARDED arms (#5334). A failing guard falls through to the next arm exactly
	// as in ordinary match lowering, so what the detector must establish is that
	// the UNGUARDED arms alone still cover every variant — off the last arm the
	// loop would otherwise re-enter with the scrutinee unchanged, a hang rather
	// than a wrong answer. Here the guarded `Cons` has an unguarded `Cons` sibling,
	// so the chain is total. `1, 0, 3` exercises both arms: 2 + 0 + 4 = 6.
	asm = run(t, `enum List { Cons(i32, List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) when h > 0 => { return Cons(h + 1, step(t)); },
        Cons(h, t) => { return Cons(0, step(t)); },
        Nil => { return Nil; },
    }
}
function score(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var xs: List = Cons(1, Cons(0, Cons(3, Nil))); if (score(step(xs)) != 6) { return 1; } return __rc_underflow(); }`,
		"trmc-guarded-arm", 0)
	if n := countSelfCalls(asm, "step"); n != 1 {
		t.Errorf("trmc-guarded-arm: %d call sites to step in the asm, want 1 (main's) — TRMC must fire on a guarded arm whose variant has an unguarded sibling", n)
	}

	// MULTI-STATEMENT arm body (#5334), matching native #5344. Only rc-NEUTRAL
	// statements qualify: the loop returns through its own `return`, which
	// bypasses the RC sweeps, so anything that could bind a reference-counted
	// value declines the transform instead of leaking it.
	asm = run(t, `enum List { Cons(i32, List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { var d: i32 = h + 1; return Cons(d, step(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(step(build(50))) != 1275) { return 1; } return __rc_underflow(); }`,
		"trmc-multi-statement-arm", 0)
	if n := countSelfCalls(asm, "step"); n != 1 {
		t.Errorf("trmc-multi-statement-arm: %d call sites to step in the asm, want 1 (main's) — TRMC must fire on a scalar-only multi-statement arm", n)
	}

	// SETUP STATEMENTS before the match, one of them an early `return`. They are
	// emitted INSIDE the loop, because the recursion re-enters them: hoisting them
	// out would freeze `lim` at its first-call value while the loop advances `n`
	// underneath it, and `take` would never terminate its take-count.
	// build(6) = [5,4,3,2,1,0]; take 3, +1 each = [6,5,4] = 15.
	asm = run(t, `enum List { Cons(i32, List), Nil }
function take(xs: List, n: i32): List {
    var lim: i32 = n;
    if (lim <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, take(t, lim - 1)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(take(build(6), 3)) != 15) { return 1; } return __rc_underflow(); }`,
		"trmc-setup-statements", 0)
	if n := countSelfCalls(asm, "take"); n != 1 {
		t.Errorf("trmc-setup-statements: %d call sites to take in the asm, want 1 (main's)", n)
	}

	// A `_` arm as the base case, and a guard clause INSIDE an arm body whose
	// `return` has to reach the hole machinery rather than the function's real
	// return. `stop_at_zero` over [3,0,4] keeps only the leading 3.
	asm = run(t, `enum List { Cons(i32, List), Nil }
function stop_at_zero(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h == 0) { return Nil; } return Cons(h, stop_at_zero(t)); },
        _ => { return Nil; },
    }
}
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var xs: List = Cons(3, Cons(0, Cons(4, Nil))); if (sum(stop_at_zero(xs)) != 3) { return 1; } return __rc_underflow(); }`,
		"trmc-wildcard-and-inner-guard", 0)
	if n := countSelfCalls(asm, "stop_at_zero"); n != 1 {
		t.Errorf("trmc-wildcard-and-inner-guard: %d call sites in the asm, want 1 (main's)", n)
	}

	// TAIL if/else whose true leaf is a BARE self-call — the filter shape, where
	// the hole stays put and only the params advance. Dropping the negatives from
	// [-5,4,-3,2,-1,0] leaves [4,2,0] = 6. 200k cells also pins that the mixed
	// cons/self loop still holds the stack flat.
	asm = run(t, `enum List { Cons(i32, List), Nil }
function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } else { return Cons(h, drop_neg(t)); } },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; var s: i32 = 1; while (i < n) { acc = Cons(i * s, acc); s = 0 - s; i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(drop_neg(build(6))) != 6) { return 1; } return __rc_underflow(); }`,
		"trmc-branch-tail-self-call", 0)
	if n := countSelfCalls(asm, "drop_neg"); n != 1 {
		t.Errorf("trmc-branch-tail-self-call: %d call sites in the asm, want 1 (main's)", n)
	}
	run(t, `enum List { Cons(i32, List), Nil }
function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } else { return Cons(h + 1, drop_neg(t)); } },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(drop_neg(build(200000))) != 400000) { return 1; } return __rc_underflow(); }`,
		"trmc-branch-tail-deep", 0)

	// The hole in the FIRST payload rather than the last: the link's field index
	// is trmc_hole_index's answer, not "the last field".
	asm = run(t, `enum List { Cons(i32, List), Nil }
enum Rev { Node(Rev, i32), End }
function to_rev(xs: List): Rev {
    match (xs) {
        Cons(h, t) => { return Node(to_rev(t), h + 1); },
        Nil => { return End; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum_rev(r: Rev): i32 { var acc: i32 = 0; var cur: Rev = r; var go: boolean = true; while (go) { match (cur) { Node(nx, v) => { acc = acc + v; cur = nx; }, End => { go = false; } } } return acc; }
function main(): i32 { if (sum_rev(to_rev(build(6))) != 21) { return 1; } return __rc_underflow(); }`,
		"trmc-hole-first-payload", 0)
	if n := countSelfCalls(asm, "to_rev"); n != 1 {
		t.Errorf("trmc-hole-first-payload: %d call sites in the asm, want 1 (main's)", n)
	}
}

// mixedCtorProg is the mixed-variant TRMC shape over `n` Cons/Neg pairs, whose
// `score` must come out `want`. Cons and Neg carry the same payload arity, so
// both recursive arms share one tail index; they are signed oppositely by
// `score`, so a node built under the wrong ctor is a wrong answer, not a
// coincidence.
func mixedCtorProg(n, want int) string {
	return fmt.Sprintf(`enum List { Cons(i32, List), Neg(i32, List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, step(t)); },
        Neg(h, t) => { return Neg(h - 1, step(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(10, Neg(10, acc)); i = i + 1; } return acc; }
function score(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Neg(h, t) => { acc = acc - h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = step(build(%d)); if (score(ys) != %d) { return 1; } return __rc_underflow(); }`, n, want)
}

// mixedScrutArityProg walks a list whose `B` cells carry one payload and whose
// `A` / `Z` cells carry two, rebuilding every cell as a two-payload node. The
// consuming traversal shallow-frees the `B` box while the loop holds an `A`
// under construction; `score` signs the three variants apart, so
// A(2) Z(4) A(7) A(4) scores 2 - 4 + 7 + 4 = 9.
func mixedScrutArityProg() string {
	return `enum L { A(i32, L), Z(i32, L), B(L), Nil }
function step(xs: L): L {
    match (xs) {
        A(h, t) => { return A(h + 1, step(t)); },
        Z(h, t) => { return Z(h - 1, step(t)); },
        B(t) => { return A(7, step(t)); },
        Nil => { return Nil; },
    }
}
function score(l: L): i32 { var acc: i32 = 0; var cur: L = l; var go: boolean = true; while (go) { match (cur) { A(h, t) => { acc = acc + h; cur = t; }, Z(h, t) => { acc = acc - h; cur = t; }, B(t) => { acc = acc + 1000; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var xs: L = A(1, Z(5, B(A(3, Nil)))); if (score(step(xs)) != 9) { return 1; } return __rc_underflow(); }`
}

// countSelfCalls counts `call __fn_<name>` sites in emitted x86-64 asm. A TRMC'd
// function keeps only its callers' call sites; the recursive ones are gone.
func countSelfCalls(asm, name string) int {
	return strings.Count(asm, "call __fn_"+name+"\n")
}

// TestSelfHostTrmcWasmIR: the wasm sibling through the -ir driver.
func TestSelfHostTrmcWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping TRMC wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"trmc-value-wasm", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = inc_all(build(50)); if (sum(ys) != 1275) { return 1; } return __rc_underflow(); }`, 0},
		{"trmc-deep-stack-wasm", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = inc_all(build(200000)); if (sum(ys) != 400000) { return 1; } return __rc_underflow(); }`, 0},
		// Mixed-variant recursive arms (#5334), value and 200k-deep, on the wasm
		// layout engine as well as the register one.
		{"trmc-mixed-ctor-wasm", mixedCtorProg(3, 6), 0},
		{"trmc-mixed-ctor-deep-wasm", mixedCtorProg(100000, 200000), 0},
		{"trmc-mixed-ctor-narrow-scrutinee-wasm", mixedScrutArityProg(), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("TRMC wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostTrmcIRArm64: the arm64 sibling under qemu — value + the deep
// O(1)-stack discriminator.
func TestSelfHostTrmcIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (139 = stack overflow → TRMC didn't fire)", name, code, want)
		}
	}

	run(t, `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { var acc: List = Nil; var i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { var acc: i32 = 0; var cur: List = l; var go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { var ys: List = inc_all(build(200000)); if (sum(ys) != 400000) { return 1; } return __rc_underflow(); }`,
		"trmc-deep-stack-arm64", 0)
}
