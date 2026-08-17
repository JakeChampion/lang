package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynArrayArgIRCases exercise a `dyn Trait[]` ARRAY LITERAL written directly at
// a call — `render([1, 22, 333])` where render's param is `dyn Sh[]` (#6906).
//
// This is the third coercion site. The two already wired detect a `dyn`
// destination from a scalar parameter's signature and from a
// `var xs: dyn Sh[] = […]` annotation; an ARRAY-typed parameter is neither, so
// the literal's elements were built into the buffer RAW. A struct/enum element
// carries its own shape and so survived that — which is why the existing
// dyn tests passed — but a primitive/string element does not, and the callee's
// op_dyn_dispatch then read a shape pointer out of a scalar: SIGSEGV on the
// register backends, a validation failure on wasm.
//
// The struct rows are the controls: they must keep working, and their presence
// is what shows the fix is about ELEMENT coercion rather than the call itself.
//
// Each case is oracle-checked against the interpreter and routing-pinned to
// "ir".
const dynArrayArgPrelude = `trait Sh { function area(self: Self): i32; }
struct C { r: i32 }
impl Sh for C { function area(self: Self): i32 { return self.r * self.r; } }
impl Sh for i32 { function area(self: Self): i32 { return self + 1; } }
impl Sh for string { function area(self: Self): i32 { return self.len(); } }
function total(xs: dyn Sh[]): i32 { var t: i32 = 0; for x in xs { t = t + x.area(); } return t; }
`

var dynArrayArgIRCases = []struct {
	name string
	main string
}{
	// The reported shape: i32 elements written as a literal at the call.
	// (1+1) + (22+1) + (333+1) = 359.
	{"arg-literal-i32", `function main(): i32 { return total([1, 22, 333]) - 300; }`},
	// String elements take the same box (the value word is the string's box
	// pointer): 2 + 3 = 5.
	{"arg-literal-string", `function main(): i32 { return total(["ab", "cde"]); }`},
	// CONTROL: struct elements carry their own shape, so they flowed in
	// correctly before the fix and must still.
	{"arg-literal-struct", `function main(): i32 { return total([C { r: 3 }, C { r: 4 }]); }`},
	// Mixed: one boxed primitive next to one shape-carrying struct.
	{"arg-literal-mixed", `function main(): i32 { return total([C { r: 5 }, 16]); }`},
	// The literal is not the first argument — the flag is read per position.
	{"arg-literal-second-param", `function twice(k: i32, xs: dyn Sh[]): i32 { return k + total(xs); }
function main(): i32 { return twice(10, [1, 2]); }`},
	// CONTROL: the same literal bound to a local first is the already-wired
	// site, and must be unaffected.
	{"arg-local-binding", `function main(): i32 { var ys: dyn Sh[] = [1, 22, 333]; return total(ys) - 300; }`},
	// Empty literal: no elements to coerce, and the buffer is still built.
	{"arg-literal-empty", `function main(): i32 { return total([]) + 7; }`},
}

// TestSelfHostDynArrayArgIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostDynArrayArgIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynArrayArgIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayArgPrelude + tc.main + "\n")
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
				t.Errorf("%s exited %d, want %d (interp oracle; 139 = the unboxed element's shape read segfaulted)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostDynArrayArgIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDynArrayArgIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-array-arg wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range dynArrayArgIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayArgPrelude + tc.main + "\n")
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
			watFile := filepath.Join(dir, "dynarrarg_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("dyn-array-arg wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
