package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynArrayLocalIRCases exercise method dispatch on the elements of a `dyn Trait[]`
// LOCAL through the self-host IR path on x86-64 + wasm.
//
// A `dyn Trait[]` PARAM already recorded the coarse `"dyn Trait"` element type on
// its slot (so `for x in param` / `param[i].m()` dispatched dynamically), but the
// local-`var` path marked the slot `is_arr` with NO element type — so every method
// call on an element of a dyn-array LOCAL (`xs[i].m()`, `var e = xs[i]; e.m()`,
// `for x in xs { x.m() }`) bailed to the AST path. The fix records the same coarse
// element type on the local slot, mirroring the param path.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a non-negative value <= 126 (cf. #2908).
const dynArrayLocalPrelude = `trait Sh { function area(self: Self): i32; }
struct C { r: i32 }
struct R { w: i32, h: i32 }
impl Sh for C { function area(self: Self): i32 { return self.r * self.r; } }
impl Sh for R { function area(self: Self): i32 { return self.w * self.h; } }
`

var dynArrayLocalIRCases = []struct {
	name string
	main string
}{
	// Inline index then method call: C{5}.area() = 25.
	{"inline-index", `function main(): i32 { var xs: dyn Sh[] = [C { r: 5 }]; return xs[0].area(); }`},
	// Bind an element, then dispatch on the binding: 25.
	{"bind-element", `function main(): i32 { var xs: dyn Sh[] = [C { r: 5 }]; var e = xs[0]; return e.area(); }`},
	// Heterogeneous local iterated in a loop: 9 + 10 = 19.
	{"loop", `function main(): i32 { var xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; var t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
	// Two inline indices added: 9 + 10 = 19.
	{"two-index", `function main(): i32 { var xs: dyn Sh[] = [C { r: 3 }, R { w: 2, h: 5 }]; return xs[0].area() + xs[1].area(); }`},
	// Three heterogeneous elements in a loop: 4 + 6 + 9 = 19.
	{"loop-three", `function main(): i32 { var xs: dyn Sh[] = [C { r: 2 }, R { w: 2, h: 3 }, C { r: 3 }]; var t: i32 = 0; for x in xs { t = t + x.area(); } return t; }`},
}

// TestSelfHostDynArrayLocalIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostDynArrayLocalIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range dynArrayLocalIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayLocalPrelude + tc.main + "\n")
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

// TestSelfHostDynArrayLocalIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDynArrayLocalIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-array-local wasm IR e2e")
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

	for _, tc := range dynArrayLocalIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(dynArrayLocalPrelude + tc.main + "\n")
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
			watFile := filepath.Join(dir, "dynarrlocal_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("dyn-array-local wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
