package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An `fn[]` struct field whose construction proves NEITHER representation has no
// safe dispatch: `(() => T)[]` is spelled the same for an array of raw function
// pointers and an array of `__mkclo$` env boxes, and calling either through the
// other's convention crashes. #5787 measured both unprovable shapes — a field
// built from a PARAM, and one built by `.append` in a LOOP — SIGSEGV'ing on the
// IR path AND on the AST fallback, with the interpreter returning 7 both times.
//
// #5790 made both classifications positive evidence, which is what makes a clean
// error possible: the compiler can now tell "proven closures" from "proven
// pointers" from "cannot tell". This gate reports the third case before either
// emit runs, the way `wasm_unsupported_builtin` reports a builtin with no wasm
// meaning. A diagnostic naming the field beats a segfault.
//
// The gate is narrowed twice so it cannot reject working programs, and both
// narrowings are pinned below: it only fires for a field this module CONSTRUCTS
// (a field built in a sibling module is not this module's call, which matters
// for per-module emit) and only for one it READS through an element (a bare
// `.len()` never reaches an element, so the representation cannot matter).
var fnArrayFieldGateRejected = []struct {
	name string
	src  string
}{
	// Built from a param: no construction site is visible at the struct literal,
	// so neither scan can prove anything. #5787's first measured crash.
	{"param-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction mk(a: (() => i32)[]): R { return R { hs: a }; }\nfunction main(): i32 { var r: R = mk([seven]); return r.hs[0](); }"},
	// Built by `.append` in a loop: the local is never bound to a proving array
	// literal. #5787's second measured crash.
	{"loop-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = []; var i: i32 = 0; while (i < 1) { a = a.append(seven); i = i + 1; } var r: R = R { hs: a }; return r.hs[0](); }"},
}

// The shapes that must keep compiling. Each is a real capability — losing any of
// them to an over-eager gate would be a worse regression than the crash it
// replaces, so they carry their expected exit codes rather than just "accepted".
var fnArrayFieldGateAccepted = []struct {
	name string
	src  string
	exit int
}{
	{"literal-pointers", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var r: R = R { hs: [seven] }; return r.hs[0](); }", 7},
	{"literal-closures", "struct R { hs: (() => i32)[] }\nfunction main(): i32 { var n: i32 = 3; var r: R = R { hs: [() => n] }; return r.hs[0](); }", 3},
	{"local-built", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; var r: R = R { hs: a }; return r.hs[0](); }", 7},
	// The read is through a PARAM receiver — the binding map has to resolve
	// `r: R` or the gate would either miss the read or misattribute it.
	{"param-receiver-read", "struct R { hs: (() => i32)[] }\nfunction seven(): i32 { return 7; }\nfunction call(r: R): i32 { return r.hs[0](); }\nfunction main(): i32 { return call(R { hs: [seven] }); }", 7},
	// Unprovable construction, but only `.len()` is read — no element is ever
	// reached, so the representation cannot matter and this must NOT be rejected.
	{"unprovable-but-len-only", "struct R { hs: (() => i32)[] }\nfunction mk(a: (() => i32)[]): R { return R { hs: a }; }\nfunction main(): i32 { var r: R = mk([]); return r.hs.len(); }", 0},
}

// TestSelfHostFnArrayFieldGateX86_64 — the x86-64 leg, through the production
// driver (asm_ir_run).
func TestSelfHostFnArrayFieldGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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

	for _, tc := range fnArrayFieldGateRejected {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			out, errOut, code := drive(tc.src)
			if code == 0 {
				t.Fatalf("compiled an unclassifiable fn[] field instead of reporting it (%d bytes of asm)", len(out))
			}
			if len(out) != 0 {
				t.Errorf("rejection still wrote %d bytes of asm", len(out))
			}
			// Naming the field is the whole point — "cannot classify" without
			// saying which field leaves the author hunting.
			if !strings.Contains(errOut, "R.hs") {
				t.Errorf("diagnostic does not name the field:\n%s", errOut)
			}
		})
	}

	for _, tc := range fnArrayFieldGateAccepted {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			asm, errOut, code := drive(tc.src)
			if code != 0 {
				t.Fatalf("rejected a working program (exit %d):\n%s", code, errOut)
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

// TestSelfHostFnArrayFieldGateWasm — the wasm leg. The gate lives in the shared
// irlower, but each driver wires it separately, so both need pinning.
func TestSelfHostFnArrayFieldGateWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the fn[]-field gate wasm leg")
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

	for _, tc := range fnArrayFieldGateRejected {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			out, errOut, code := drive(tc.src)
			if code == 0 {
				t.Fatalf("compiled an unclassifiable fn[] field instead of reporting it (%d bytes of wat)", len(out))
			}
			if !strings.Contains(errOut, "R.hs") {
				t.Errorf("diagnostic does not name the field:\n%s", errOut)
			}
		})
	}

	for _, tc := range fnArrayFieldGateAccepted {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			wat, errOut, code := drive(tc.src)
			if code != 0 {
				t.Fatalf("rejected a working program (exit %d):\n%s", code, errOut)
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
