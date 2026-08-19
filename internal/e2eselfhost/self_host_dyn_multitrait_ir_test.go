package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynMultiTraitIRCases exercise MULTI-trait trait objects `dyn A + B` through
// the stack-IR path (docs/DYN-TRAITS.md §10). The self-host erases the whole
// trait set to a coarse `"dyn A + B"` spelling and dispatches each method call
// by the RECEIVER'S RUNTIME SHAPE (op_dyn_dispatch enumerates every module
// method of the called name over its struct/prim receivers — trait-agnostic),
// so a method declared by ANY trait in the set lands on the concrete impl with
// no codegen change beyond parsing `+`. These cases call a method from EACH
// trait on the same `dyn A + B` value, so the exit-code oracle proves both
// dispatched.
var dynMultiTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The motivating §10 case: `dyn Show + Weigh`, calling show() (from Show)
	// AND weigh() (from Weigh) on the same value. show()=10, weigh()=w=3 →
	// 10 + 3 = 13. Both traits must dispatch or the sum is wrong.
	{"two-trait-scalar",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } function describe(x: dyn Show + Weigh): i32 { return x.show() + x.weigh(); } function main(): i32 { var a: Apple = Apple { w: 3 }; return describe(a); }`, 13},
	// Order-insensitive: `dyn Weigh + Show` (the other source order) must behave
	// identically — dispatch is set-agnostic. Same value, same result 13.
	{"two-trait-scalar-reordered",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } function describe(x: dyn Weigh + Show): i32 { return x.show() + x.weigh(); } function main(): i32 { var a: Apple = Apple { w: 3 }; return describe(a); }`, 13},
	// HETEROGENEOUS `dyn Show + Weigh[]`: two concrete types, BOTH impl-ing BOTH
	// traits, iterated + each dispatched on show()+weigh(). Apple{w:3}: 10+3=13;
	// Brick{kg:5}: 20+5=25. Sum = 38.
	{"two-trait-array",
		`trait Show { function show(self: Self): i32; } trait Weigh { function weigh(self: Self): i32; } struct Apple { w: i32 } struct Brick { kg: i32 } impl Show for Apple { function show(self: Self): i32 { return 10; } } impl Weigh for Apple { function weigh(self: Self): i32 { return self.w; } } impl Show for Brick { function show(self: Self): i32 { return 20; } } impl Weigh for Brick { function weigh(self: Self): i32 { return self.kg; } } function total(xs: dyn Show + Weigh[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show() + x.weigh(); } return t; } function main(): i32 { var xs: dyn Show + Weigh[] = [Apple { w: 3 }, Brick { kg: 5 }]; return total(xs); }`, 38},
	// THREE traits: `dyn A + B + C`, a method from EACH. a()=1, b()=2*v, c()=100.
	// v=4 → 1 + 8 + 100 = 109.
	{"three-trait-scalar",
		`trait A { function a(self: Self): i32; } trait B { function b(self: Self): i32; } trait C { function c(self: Self): i32; } struct T { v: i32 } impl A for T { function a(self: Self): i32 { return 1; } } impl B for T { function b(self: Self): i32 { return self.v * 2; } } impl C for T { function c(self: Self): i32 { return 100; } } function f(x: dyn A + B + C): i32 { return x.a() + x.b() + x.c(); } function main(): i32 { var t: T = T { v: 4 }; return f(t); }`, 109},
	// A multi-trait method taking an ARGUMENT, dispatched dynamically across the
	// set. scale() (from Sc) * k=3, plus tag() (from Tag). v=5: 5*3 + 7 = 22.
	{"two-trait-method-arg",
		`trait Sc { function scale(self: Self, k: i32): i32; } trait Tag { function tag(self: Self): i32; } struct W { v: i32 } impl Sc for W { function scale(self: Self, k: i32): i32 { return self.v * k; } } impl Tag for W { function tag(self: Self): i32 { return 7; } } function f(x: dyn Sc + Tag): i32 { return x.scale(3) + x.tag(); } function main(): i32 { var w: W = W { v: 5 }; return f(w); }`, 22},
	// SINGLE-trait `dyn Show` through the SAME harness — the 1-element regression
	// gate: must lower/behave exactly as before the multi-trait parse change.
	// show() = 16.
	{"single-trait-regression",
		`trait Show { function show(self: Self): i32; } struct Circle { r: i32 } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function f(s: dyn Show): i32 { return s.show(); } function main(): i32 { var c: Circle = Circle { r: 4 }; return f(c); }`, 16},
}

// TestSelfHostDynMultiTraitIRX86_64 routes each multi-trait case through the
// self-hosted x86-64 driver (asm_run) and asserts the exit code, AND probes the
// routing (asm_pathprobe_run) to pin each case to the "ir" path — proving the
// multi-trait `dyn A + B` value flows through IR lowering.
func TestSelfHostDynMultiTraitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynMultiTraitIRCases {
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

// TestSelfHostDynMultiTraitIRWasm runs the same multi-trait cases through the
// wasm IR backend (wasm_ir_run -ir): emit_dyn_dispatch is likewise method-name
// based (a struct-id compare-branch over every impl of the called method), so a
// method from any trait in the set dispatches with no codegen change.
func TestSelfHostDynMultiTraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host multi-trait dyn wasm IR e2e")
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

	for _, tc := range dynMultiTraitIRCases {
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
			watFile := filepath.Join(dir, "dynmultitrait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("multi-trait dyn wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
