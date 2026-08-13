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
// payload arity, which is what makes the tail index a single constant. Arms whose
// ctors disagree on arity, tree-shaped bodies (two self-calls), multi-statement
// arm bodies and non-last-hole shapes keep the plain recursion — the detector
// bails, which countSelfCalls witnesses.
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

	// EXCLUDED — recursive ctors of different payload ARITY. The hole is filled by
	// an op_struct_set at a compile-time field index, so the tail cannot sit at
	// index 1 in one iteration and index 0 in the next; the detector bails and the
	// self-calls stay in the asm.
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
		"trmc-mixed-arity-not-rewritten", 0)
	if n := countSelfCalls(asm, "step"); n < 2 {
		t.Errorf("trmc-mixed-arity-not-rewritten: %d call sites to step in the asm, want more than main's one (TRMC must not fire on mixed arities)", n)
	}

	// EXCLUDED — a multi-statement arm body. Native's detectTrmc requires the arm
	// to be a single `return` too; admitting leading statements means lowering
	// them inside a body that deliberately runs without the RC sweeps, so it waits
	// on the sibling native widening (#4402) rather than diverging here.
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
		"trmc-multi-statement-arm-not-rewritten", 0)
	if n := countSelfCalls(asm, "step"); n < 2 {
		t.Errorf("trmc-multi-statement-arm-not-rewritten: %d call sites to step in the asm, want more than main's one (TRMC must not fire on a multi-statement arm)", n)
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
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
