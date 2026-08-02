package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynFnParamIRCases exercise a `dyn Trait` value dispatched THROUGH a
// function-typed parameter — a `(dyn Trait) => R` fn value called with a
// trait-object argument — on the self-host IR path (x86-64 + wasm).
//
// The `(dyn Trait) => R` fn-type spelling parses to the coarse "fn" tag
// (#5273 restored the parse after #5267's paren-unwrap swallowed the `=> R`).
// #5276 reported that the value still miscompiled through the fn param, but
// the shapes below all lower correctly on the IR path:
//
//   - a struct-backed `dyn Trait` value flows UNBOXED (it already carries its
//     shape pointer at offset 0), so passing it through `f(x)` — whether the
//     value arrives pre-coerced in a `dyn Trait` param or is coerced inline at
//     the fn-value call site — carries the shape and `s.area()` dispatches via
//     op_dyn_dispatch inside the callee;
//   - a primitive-backed `dyn Trait` value coerced at the OUTER call
//     (`apply(speak_of, q)` where `q: dyn Speak`) is heap-boxed at that call
//     (callee_param_is_dyn on `apply`), so the box pointer flows through `f(x)`
//     unchanged and dispatches correctly.
//
// The remaining un-lowered shape — a PRIMITIVE literal coerced to `dyn Trait`
// AT an indirect fn-value call (`f(7)` where `f: (dyn Speak) => i32`) — needs
// the fn-type's per-parameter dyn-ness threaded through parse_type_name (which
// coarsens `(dyn Speak) => i32` to the flat "fn" tag, discarding it), the
// FuncDecl param, the slot, and the indirect-call arg lowering. That is a
// separate, broader IR-widening item (a `callee_param_is_dyn` for fn-value
// params), tracked as a #5276 follow-up; it is NOT one of these cases.
//
// Each case is oracle-checked against the interpreter and routing-pinned to
// "ir", returning a non-negative value <= 126 (cf. #2908).
var dynFnParamIRCases = []struct {
	name string
	main string
}{
	// The #5276 repro: a struct-backed `dyn Shape` value coerced at the outer
	// call, dispatched through the `(dyn Shape) => i32` fn param. 4*4 = 16.
	{"repro-struct-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { var q: dyn Shape = Sq{s:4}; return apply(area_of, q); }`},
	// Two impls so dispatch is meaningful, value via the `dyn Shape` param:
	// Rect{3,5}.area() = 15.
	{"two-impl-via-param", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32, x: dyn Shape): i32 { return f(x); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { var q: dyn Shape = Rect{w:3,h:5}; return apply(area_of, q); }`},
	// A struct value coerced to `dyn Shape` INLINE at the indirect fn-value
	// call (`f(Rect{..})`), no intermediate `dyn` param: 3*5 = 15.
	{"struct-inline-at-indirect", `trait Shape { function area(self: Self): i32; }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function apply(f: (dyn Shape) => i32): i32 { return f(Rect{w:3,h:5}); }
function area_of(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { return apply(area_of); }`},
	// A primitive-backed `dyn Speak` value: boxed at the outer `apply(speak_of, q)`
	// call, dispatched through the fn param. 7 + 100 = 107.
	{"prim-via-param", `trait Speak { function say(self: Self): i32; }
impl Speak for i32 { function say(self: Self): i32 { return self + 100; } }
function apply(f: (dyn Speak) => i32, x: dyn Speak): i32 { return f(x); }
function speak_of(s: dyn Speak): i32 { return s.say(); }
function main(): i32 { var q: dyn Speak = 7; return apply(speak_of, q); }`},
}

// TestSelfHostDynFnParamIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostDynFnParamIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynFnParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostDynFnParamIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDynFnParamIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-fn-param wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range dynFnParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "dynfnparam_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("dyn-fn-param wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
