package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynTraitIRCases exercise `dyn Trait` method dispatch through the stack-IR
// path. A `dyn Trait` value's concrete type is known only at runtime, so
// `x.method()` is a DYNAMIC dispatch: the receiver (+ args) are spilled into
// temp locals and op_dyn_dispatch reads the receiver's runtime shape pointer,
// dispatching to the matching `<ConcreteType>.<method>` via a compare-branch
// chain over the trait's impl types (the same shape `match`/`variant_is`
// reads). Exit codes are the oracle.
var dynTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The motivating case: a heterogeneous `dyn Shape[]` iterated in a loop.
	// 3*3 + 2*5 = 9 + 10 = 19.
	{"heterogeneous-array",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }]; return sum(xs); }`, 19},
	// A `dyn Shape` SCALAR param, receiving a Circle. 4*4 = 16.
	{"param-circle",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } function main(): i32 { var c: Circle = Circle { r: 4 }; return ar(c); }`, 16},
	// Same param, receiving a Rect — the OTHER impl arm. 2*5 = 10.
	{"param-rect",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } function ar(s: dyn Shape): i32 { return s.area(); } function main(): i32 { var r: Rect = Rect { w: 2, h: 5 }; return ar(r); }`, 10},
	// A trait method taking an ARGUMENT, dispatched dynamically. 5 * 3 = 15.
	{"method-with-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } struct A { v: i32 } struct B { v: i32 } impl Sc for A { function sc(self: Self, k: i32): i32 { return self.v * k; } } impl Sc for B { function sc(self: Self, k: i32): i32 { return self.v + k; } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var a: A = A { v: 5 }; return f(a); }`, 15},
	// Three impls in a heterogeneous array — exercises a longer compare-branch
	// chain. 3*3 + 2*5 + 7 = 9 + 10 + 7 = 26.
	{"three-impls",
		`trait Shape { function area(self: Self): i32; } struct Circle { r: i32 } struct Rect { w: i32, h: i32 } struct Unit { } impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } } impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } } impl Shape for Unit { function area(self: Self): i32 { return 7; } } function sum(xs: dyn Shape[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; } function main(): i32 { var xs: dyn Shape[] = [Circle { r: 3 }, Rect { w: 2, h: 5 }, Unit { }]; return sum(xs); }`, 26},

	// --- `dyn` over PRIMITIVE / string receivers (docs/DYN-TRAITS.md §4.2.3) ---
	// A primitive value has no shape pointer, so it is heap-boxed at the coercion
	// site into a `dyn` cell [shape@0, value@8] (op_dyn_box); dispatch matches the
	// box's offset-0 shape (the interned primitive type name / id) and UNBOXES the
	// value from offset 8 before calling `<prim>.<method>`.

	// `dyn` over i32, SCALAR param. The arg 41 is boxed at the call; show() adds 1.
	{"prim-i32-scalar",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self + 1; } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var x: i32 = 41; return run(x); }`, 42},
	// `dyn` over i32 with a method ARGUMENT. 5 * 3 = 15 (the unboxed receiver 5,
	// the plain arg 3).
	{"prim-i32-method-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } impl Sc for i32 { function sc(self: Self, k: i32): i32 { return self * k; } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var a: i32 = 5; return f(a); }`, 15},
	// `dyn` over `string`: the value is a one-word string-box pointer, boxed like
	// any primitive. show() returns its length. len("hello") = 5.
	{"prim-string",
		`trait Show { function show(self: Self): i32; } impl Show for string { function show(self: Self): i32 { return self.len(); } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var x: string = "hello"; return run(x); }`, 5},
	// A homogeneous `dyn`-over-i32 ARRAY: each element is boxed at the array
	// literal, then iterated + dispatched through runtime shape. 3 + 4 + 5 = 12.
	{"prim-i32-array",
		`trait Show { function show(self: Self): i32; } impl Show for i32 { function show(self: Self): i32 { return self; } } function sum(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { var xs: dyn Show[] = [3, 4, 5]; return sum(xs); }`, 12},
}

// TestSelfHostDynTraitIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostDynTraitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynTraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostDynTraitIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir): the shape is a numeric type id (i32.load @0), and the
// dispatch is a nested if/else chain.
func TestSelfHostDynTraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-trait wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range dynTraitIRCases {
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
			watFile := filepath.Join(dir, "dyntrait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-trait wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
