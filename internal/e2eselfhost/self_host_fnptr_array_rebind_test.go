package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fnptrArrayRebindCases pin a whole-literal REBIND of a fn-POINTER array local —
// `var a: (() => i32)[] = [seven]; a = [nine];` — which SIGSEGV'd on the x86-64
// IR path (and trapped on wasm) while the interpreter returned the right answer.
//
// A `(() => T)[]` local carries a representation flag (`is_fnarr`) set where it
// is DECLARED, and a bare fn NAME is only lowered to a fn POINTER (`const_func`)
// at the sites that know about that flag: the declaration, and `.append`. The
// whole-literal reassignment was the third construction site and had no such
// handling, so its elements fell through to the generic expression path, which
// const-CALLS a 0-arg fn name and stores the RESULT. The buffer then held
// integers where code addresses belong, and the plain `call_indirect` the
// is_fnarr slot dispatches through jumped to 9.
//
// The fix is representation-PRESERVING: the slot is already is_fnarr and stays
// so. That is what makes the branch and loop cases below correct too — the
// lowering never has to decide whether the assignment dominates the reads.
//
// A rebind that CHANGES the representation (`[seven]` <-> `[() => n]`, either
// direction) is a separate, still-open defect — see docs/SELFHOST-AST-RETIREMENT.md.
//
// Exit codes cross-checked against the interpreter.
var fnptrArrayRebindCases = []struct {
	name string
	src  string
	exit int
}{
	// The base shape: rebind, then call element 0.
	{"rebind", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; a = [nine]; return a[0](); }", 9},
	// Two elements, order swapped — pins per-element pointer identity rather
	// than "the whole buffer happens to be right".
	{"rebind-multi", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction main(): i32 { var a: (() => i32)[] = [seven, nine]; a = [nine, seven]; return a[0]() * 10 + a[1](); }", 97},
	// The rebind is inside a branch that does NOT run: the declaration's buffer
	// is the one called. Representation-preserving lowering means this needs no
	// dominance reasoning.
	{"rebind-in-branch", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction main(): i32 { var n: i32 = 5; var a: (() => i32)[] = [seven]; if (n > 100) { a = [nine]; } return a[0](); }", 7},
	// …and inside a loop that runs twice, so the rebind is re-executed.
	{"rebind-in-loop", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; var i: i32 = 0; while (i < 2) { a = [nine]; i = i + 1; } return a[0](); }", 9},
	// Rebind then grow: the `.append` path (which always emitted const_func)
	// must agree with the buffer the rebind just built.
	{"rebind-then-append", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction main(): i32 { var a: (() => i32)[] = [seven]; a = [nine]; a = a.append(seven); return a[0]() * 10 + a[1](); }", 97},
	// Repeated rebinds in a loop: the superseded buffer is released by
	// emit_arr_store's cow-guarded dec, so the heap must not grow without bound
	// and nothing may be released twice.
	{"rebind-rc-soundness", "function seven(): i32 { return 7; }\nfunction nine(): i32 { return 9; }\nfunction churn(n: i32): i32 { var a: (() => i32)[] = [seven]; var i: i32 = 0; var s: i32 = 0; while (i < n) { a = [nine]; s = a[0](); i = i + 1; } return s; }\nfunction main(): i32 { var w: i32 = churn(3000); var b1: i32 = __heap_bump_bytes(); var x: i32 = churn(3000); var b2: i32 = __heap_bump_bytes(); if (__rc_underflow() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return w; }", 9},
}

// TestSelfHostFnptrArrayRebindIRX86_64 — the x86-64 leg, through the production
// driver (asm_ir_run `-ir`).
func TestSelfHostFnptrArrayRebindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFnptrArrayRebindIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared irlower.fern, so the arm64 IR backend picks it up; this pins it.
func TestSelfHostFnptrArrayRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 fnptr-array-rebind gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnptrArrayRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostFnptrArrayRebindIRWasm — the wasm IR leg. All exit codes are
// <= 120 (the wasm exit-code clamp).
func TestSelfHostFnptrArrayRebindIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fnptr-array-rebind wasm IR e2e")
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

	for _, tc := range fnptrArrayRebindCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "fnptr_rebind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("fnptr-array rebind wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
