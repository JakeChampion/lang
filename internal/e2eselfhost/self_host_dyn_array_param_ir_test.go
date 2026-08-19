package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynArrayParamIRCases exercise method dispatch on the elements of a
// parenthesized `(dyn Trait)[]` PARAM through the self-host IR path on
// x86-64 + wasm.
//
// A `dyn Trait[]` param (no parens) already recorded the coarse `"dyn Trait"`
// element type on its slot, so `param[i].m()` / `for x in param` dispatched
// dynamically on the IR path. But the parenthesized spelling `(dyn Trait)[]`
// — the form the checker requires to bind the trailing `[]` to the whole
// trait object — was char-mashed by parse_type_name into `"(dynShape)[]"`
// (the space dropped, the parens kept), so the `type_name[0:4] == "dyn "`
// classifier missed and every element method call on such a param bailed to
// the AST path and miscompiled (#5203). The fix unwraps the redundant parens
// at parse time so `(dyn Trait)[]` normalises to `dyn Trait[]`.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a non-negative value <= 126 (cf. #2908).
const dynArrayParamPrelude = `trait Sh { function area(self: Self): i32; }
struct C { r: i32 }
struct R { w: i32, h: i32 }
impl Sh for C { function area(self: Self): i32 { return self.r * self.r; } }
impl Sh for R { function area(self: Self): i32 { return self.w * self.h; } }
`

var dynArrayParamIRCases = []struct {
	name string
	main string
}{
	// Direct-index dispatch on a `(dyn Sh)[]` param: 9 + 10 = 19.
	{"param-two-index", `function total(xs: (dyn Sh)[]): i32 { return xs[0].area() + xs[1].area(); }
function main(): i32 { var s: (dyn Sh)[] = [C { r: 3 }, R { w: 2, h: 5 }]; return total(s); }`},
	// while-loop index dispatch on a `(dyn Sh)[]` param: 9 + 10 = 19.
	{"param-while", `function total(xs: (dyn Sh)[]): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < xs.len()) { acc = acc + xs[i].area(); i = i + 1; } return acc; }
function main(): i32 { var s: (dyn Sh)[] = [C { r: 3 }, R { w: 2, h: 5 }]; return total(s); }`},
	// for-in loop dispatch on a `(dyn Sh)[]` param: 4 + 6 + 9 = 19.
	{"param-for", `function total(xs: (dyn Sh)[]): i32 { var acc: i32 = 0; for x in xs { acc = acc + x.area(); } return acc; }
function main(): i32 { var s: (dyn Sh)[] = [C { r: 2 }, R { w: 2, h: 3 }, C { r: 3 }]; return total(s); }`},
	// Scalar `(dyn Sh)` param (parenthesized, non-array): R{w:2,h:5}.area() = 10.
	{"param-scalar", `function one(x: (dyn Sh)): i32 { return x.area(); }
function main(): i32 { var v: dyn Sh = R { w: 2, h: 5 }; return one(v); }`},
}

// TestSelfHostDynArrayParamIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostDynArrayParamIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynArrayParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayParamPrelude + tc.main + "\n")
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

// TestSelfHostDynFnTypeParamParsesX86_64 guards a parse regression introduced
// by #5267's paren-unwrap. A `(dyn Trait) => R` FUNCTION type — the arrow
// follows the parens, so the parenthesized `dyn Trait` is the fn's PARAMETER,
// not a grouped trait-object array — must still parse. #5267 unwrapped any
// `(dyn T)` unconditionally, swallowing the `)` and dropping the trailing
// `=> R`, so the shape failed to parse (P001, zero bytes emitted). The fix
// only unwraps when `=>` does NOT follow the `)`, otherwise falling through to
// the `(T) => R` fn-type path (coarse "fn").
//
// Runtime dispatch through a dyn-trait-typed fn param is a separate pre-existing
// gap (the module does not lower), so this asserts
// the module PARSES + COMPILES (non-empty asm), not its exit value.
func TestSelfHostDynFnTypeParamParsesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	asmRun, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), asmRun, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	src := []byte(dynArrayParamPrelude +
		"function apply(f: (dyn Sh) => i32, x: dyn Sh): i32 { return f(x); }\n" +
		"function area_of(s: dyn Sh): i32 { return s.area(); }\n" +
		"function main(): i32 { var q: dyn Sh = R { w: 2, h: 5 }; return apply(area_of, q); }\n")
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for a `(dyn Trait) => R` param — parse regression (#5267 follow-up)")
	}
}

// TestSelfHostDynArrayParamIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDynArrayParamIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-array-param wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range dynArrayParamIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayParamPrelude + tc.main + "\n")
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
			watFile := filepath.Join(dir, "dynarrparam_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("dyn-array-param wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
