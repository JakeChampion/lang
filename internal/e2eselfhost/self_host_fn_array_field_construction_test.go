package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fnArrayFieldConstructionCases build an `fn[]` struct field every way #5787
// measured and call through an element. While a function array could hold raw
// function pointers or `__mkclo$` env boxes under the same spelling, a field
// built from a PARAM or by `.append` in a LOOP proved neither representation:
// both SIGSEGV'd on the IR path and the AST emitter, and later a pre-emit gate
// refused them. Since #10076 every function array holds env boxes whatever
// built it, so each shape compiles and answers what the interpreter does.
var fnArrayFieldConstructionCases = []struct {
	name string
	src  string
	exit int
}{
	// Built from a param: no construction site is visible at the struct literal.
	{"param-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction mk(a: (() => i32)[]): R { return R { hs: a }; }\nfunction main(): i32 { var r: R = mk([seven]); return r.hs[0](); }", 7},
	// Built by `.append` in a loop: the local is never bound to an array literal.
	{"loop-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = []; var i: i32 = 0; while (i < 1) { a = a.append(seven); i = i + 1; } var r: R = R { hs: a }; return r.hs[0](); }", 7},
	{"literal-named", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var r: R = R { hs: [seven] }; return r.hs[0](); }", 7},
	{"literal-closures", "struct R { hs: (() => i32)[] }\nfunction main(): i32 { var n: i32 = 3; var r: R = R { hs: [() => n] }; return r.hs[0](); }", 3},
	{"local-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; var r: R = R { hs: a }; return r.hs[0](); }", 7},
	// The read is through a PARAM receiver.
	{"param-receiver-read", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction call(r: R): i32 { return r.hs[0](); }\nfunction main(): i32 { return call(R { hs: [seven] }); }", 7},
	// Built from a param, and only `.len()` is read.
	{"param-built-len-only", "struct R { hs: (() => i32)[] }\nfunction mk(a: (() => i32)[]): R { return R { hs: a }; }\nfunction main(): i32 { var r: R = mk([]); return r.hs.len(); }", 0},
}

// TestSelfHostFnArrayFieldConstructionX86_64 — the x86-64 leg, through the production
// driver (asm_ir_run).
func TestSelfHostFnArrayFieldConstructionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	drive := func(src string) (string, string, int) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, _ := cmd.Output()
		return string(out), errBuf.String(), cmd.ProcessState.ExitCode()
	}

	for _, tc := range fnArrayFieldConstructionCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, errOut, code := drive(tc.src)
			if code != 0 {
				t.Fatalf("compile failed (exit %d):\n%s", code, errOut)
			}
			if len(asm) == 0 {
				t.Fatal("emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

// TestSelfHostFnArrayFieldConstructionWasm — the wasm leg.
func TestSelfHostFnArrayFieldConstructionWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the fn[]-field construction wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	drive := func(src string) (string, string, int) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, _ := cmd.Output()
		return string(out), errBuf.String(), cmd.ProcessState.ExitCode()
	}

	for _, tc := range fnArrayFieldConstructionCases {
		t.Run(tc.name, func(t *testing.T) {
			wat, errOut, code := drive(tc.src)
			if code != 0 {
				t.Fatalf("compile failed (exit %d):\n%s", code, errOut)
			}
			watFile := filepath.Join(dir, "fnfld_gate_prog.wat")
			if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}
