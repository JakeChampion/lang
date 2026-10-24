package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTrmcIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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

	run(t, `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = inc_all(build(50)); if (sum(ys) != 1275) { return 1; } return __rc_underflow_count(); }`,
		"trmc-value", 0)

	run(t, `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = inc_all(build(300000)); if (sum(ys) != 600000) { return 1; } return __rc_underflow_count(); }`,
		"trmc-deep-stack", 0)

	run(t, `enum SList { SCons(string, SList), SNil }
function tag_all(xs: SList): SList {
    match (xs) {
        SCons(h, t) => { return SCons(h + "!", tag_all(t)); },
        SNil => { return SNil; },
    }
}
function len_all(l: SList): i32 { let acc: i32 = 0; let cur: SList = l; let go: boolean = true; while (go) { match (cur) { SCons(h, t) => { acc = acc + h.len(); cur = t; }, SNil => { go = false; } } } return acc; }
function main(): i32 { let xs: SList = SCons("ab", SCons("cde", SNil)); let ys: SList = tag_all(xs); if (len_all(ys) != 7) { return 1; } return __rc_underflow_count(); }`,
		"trmc-string-head", 0)

	run(t, `enum Tree { Node(Tree, Tree), Leaf(i32) }
function deep(t: Tree): Tree {
    match (t) {
        Node(l, r) => { return Node(deep(l), deep(r)); },
        Leaf(v) => { return Leaf(v + 1); },
    }
}
function main(): i32 { let t: Tree = Node(Leaf(1), Node(Leaf(2), Leaf(3))); let u: Tree = deep(t); match (u) { Node(a, b) => { match (a) { Leaf(x) => { return x; }, Node(c, d) => { return 90; } } }, Leaf(y) => { return 91; } } return 92; }`,
		"trmc-tree-not-rewritten", 2)

	run(t, mixedCtorProg(3, 6), "trmc-mixed-ctor-value", 0)

	run(t, mixedCtorProg(100000, 200000), "trmc-mixed-ctor-deep", 0)

	run(t, mixedScrutArityProg(), "trmc-mixed-ctor-narrow-scrutinee", 0)

	asm := run(t, `enum List { Cons(i32, List), Wrap(List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, step(t)); },
        Wrap(t) => { return Wrap(step(t)); },
        Nil => { return Nil; },
    }
}
function score(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Wrap(t) => { acc = acc + 100; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let xs: List = Cons(1, Wrap(Cons(2, Nil))); if (score(step(xs)) != 105) { return 1; } return __rc_underflow_count(); }`,
		"trmc-mixed-hole-pos-not-rewritten", 0)
	if n := countSelfCalls(asm, "step"); n < 2 {
		t.Errorf("trmc-mixed-hole-pos-not-rewritten: %d call sites to step in the asm, want more than main's one (TRMC must not fire when the hole position differs between arms)", n)
	}

	asm = run(t, `enum List { Cons(i32, List), Nil }
@noinline function step(xs: List): List {
    match (xs) {
        Cons(h, t) when h > 0 => { return Cons(h + 1, step(t)); },
        Cons(h, t) => { return Cons(0, step(t)); },
        Nil => { return Nil; },
    }
}
function score(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let xs: List = Cons(1, Cons(0, Cons(3, Nil))); if (score(step(xs)) != 6) { return 1; } return __rc_underflow_count(); }`,
		"trmc-guarded-arm", 0)
	if n := countSelfCalls(asm, "step"); n != 1 {
		t.Errorf("trmc-guarded-arm: %d call sites to step in the asm, want 1 (main's) — TRMC must fire on a guarded arm whose variant has an unguarded sibling", n)
	}

	asm = run(t, `enum List { Cons(i32, List), Nil }
@noinline function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { let d: i32 = h + 1; return Cons(d, step(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(step(build(50))) != 1275) { return 1; } return __rc_underflow_count(); }`,
		"trmc-multi-statement-arm", 0)
	if n := countSelfCalls(asm, "step"); n != 1 {
		t.Errorf("trmc-multi-statement-arm: %d call sites to step in the asm, want 1 (main's) — TRMC must fire on a scalar-only multi-statement arm", n)
	}

	asm = run(t, `enum List { Cons(i32, List), Nil }
@noinline function take(xs: List, n: i32): List {
    let lim: i32 = n;
    if (lim <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, take(t, lim - 1)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(take(build(6), 3)) != 15) { return 1; } return __rc_underflow_count(); }`,
		"trmc-setup-statements", 0)
	if n := countSelfCalls(asm, "take"); n != 1 {
		t.Errorf("trmc-setup-statements: %d call sites to take in the asm, want 1 (main's)", n)
	}

	asm = run(t, `enum List { Cons(i32, List), Nil }
@noinline function stop_at_zero(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h == 0) { return Nil; } return Cons(h, stop_at_zero(t)); },
        _ => { return Nil; },
    }
}
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let xs: List = Cons(3, Cons(0, Cons(4, Nil))); if (sum(stop_at_zero(xs)) != 3) { return 1; } return __rc_underflow_count(); }`,
		"trmc-wildcard-and-inner-guard", 0)
	if n := countSelfCalls(asm, "stop_at_zero"); n != 1 {
		t.Errorf("trmc-wildcard-and-inner-guard: %d call sites in the asm, want 1 (main's)", n)
	}

	asm = run(t, `enum List { Cons(i32, List), Nil }
@noinline function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } else { return Cons(h, drop_neg(t)); } },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; let s: i32 = 1; while (i < n) { acc = Cons(i * s, acc); s = 0 - s; i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(drop_neg(build(6))) != 6) { return 1; } return __rc_underflow_count(); }`,
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
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { if (sum(drop_neg(build(200000))) != 400000) { return 1; } return __rc_underflow_count(); }`,
		"trmc-branch-tail-deep", 0)

	asm = run(t, `enum List { Cons(i32, List), Nil }
enum Rev { Node(Rev, i32), End }
@noinline function to_rev(xs: List): Rev {
    match (xs) {
        Cons(h, t) => { return Node(to_rev(t), h + 1); },
        Nil => { return End; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum_rev(r: Rev): i32 { let acc: i32 = 0; let cur: Rev = r; let go: boolean = true; while (go) { match (cur) { Node(nx, v) => { acc = acc + v; cur = nx; }, End => { go = false; } } } return acc; }
function main(): i32 { if (sum_rev(to_rev(build(6))) != 21) { return 1; } return __rc_underflow_count(); }`,
		"trmc-hole-first-payload", 0)
	if n := countSelfCalls(asm, "to_rev"); n != 1 {
		t.Errorf("trmc-hole-first-payload: %d call sites in the asm, want 1 (main's)", n)
	}
}

func mixedCtorProg(n, want int) string {
	return fmt.Sprintf(`enum List { Cons(i32, List), Neg(i32, List), Nil }
function step(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, step(t)); },
        Neg(h, t) => { return Neg(h - 1, step(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(10, Neg(10, acc)); i = i + 1; } return acc; }
function score(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Neg(h, t) => { acc = acc - h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = step(build(%d)); if (score(ys) != %d) { return 1; } return __rc_underflow_count(); }`, n, want)
}

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
function score(l: L): i32 { let acc: i32 = 0; let cur: L = l; let go: boolean = true; while (go) { match (cur) { A(h, t) => { acc = acc + h; cur = t; }, Z(h, t) => { acc = acc - h; cur = t; }, B(t) => { acc = acc + 1000; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let xs: L = A(1, Z(5, B(A(3, Nil)))); if (score(step(xs)) != 9) { return 1; } return __rc_underflow_count(); }`
}

func countSelfCalls(asm, name string) int {
	return strings.Count(asm, "call __fn_"+name+"\n") + strings.Count(asm, "call __fn_"+name+".r\n") + strings.Count(asm, "jmp __fn_"+name+".r\n")
}

func TestSelfHostTrmcWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping TRMC wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

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
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = inc_all(build(50)); if (sum(ys) != 1275) { return 1; } return __rc_underflow_count(); }`, 0},
		{"trmc-deep-stack-wasm", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = inc_all(build(200000)); if (sum(ys) != 400000) { return 1; } return __rc_underflow_count(); }`, 0},
		{"trmc-mixed-ctor-wasm", mixedCtorProg(3, 6), 0},
		{"trmc-mixed-ctor-deep-wasm", mixedCtorProg(100000, 200000), 0},
		{"trmc-mixed-ctor-narrow-scrutinee-wasm", mixedScrutArityProg(), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

func TestSelfHostTrmcIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 { let ys: List = inc_all(build(200000)); if (sum(ys) != 400000) { return 1; } return __rc_underflow_count(); }`,
		"trmc-deep-stack-arm64", 0)
}
