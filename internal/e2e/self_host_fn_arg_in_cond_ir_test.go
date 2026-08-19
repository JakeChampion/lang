package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #2686 tail — a fn-typed CALL ARGUMENT inside a STATEMENT-CONDITION position
// (an `if (…)` / `while (…)` condition, or a `for x in …` iterated expression)
// must be env-boxed by the self-host IR lift pass, exactly as it already is in a
// `var` initialiser / `return` / expr-statement / assignment. The lift pass
// (lift_inline_closures_stmts) used to recurse only the nested BODIES of those
// statements, never the condition / iterated expression, so a lambda argument
// there stayed a bare function pointer while the callee's fn-param — marked a
// closure local — unpacked an env box from it and crashed. These cases pin the
// fix across the three condition positions on both the native backends and the
// self-host IR path.
var fnArgInCondCases = []struct {
	name string
	main string
	want int
}{
	// fn-typed arg inside an `if` condition. apply(<lambda>, 3) -> 3<10 -> true.
	{"if-cond", `function apply(f: (i32) => boolean, x: i32): boolean { return f(x); }
function main(): i32 { if (apply(function (x: i32): boolean { return x < 10; }, 3)) { return 5; } return 0; }`, 5},
	// fn-typed arg inside a `while` condition. Loops while i<3 -> i ends at 3.
	{"while-cond", `function apply(f: (i32) => boolean, x: i32): boolean { return f(x); }
function main(): i32 { var i = 0; while (apply(function (x: i32): boolean { return x < 3; }, i)) { i = i + 1; } return i; }`, 3},
	// fn-typed arg inside a `for x in <iter>` iterated expression. pick doubles
	// 0,1,2 -> [0,2,4]; summed = 6.
	{"for-iter", `function pick(f: (i32) => i32): i32[] { var out: i32[] = []; var i = 0; while (i < 3) { out = out.append(f(i)); i = i + 1; } return out; }
function main(): i32 { var s = 0; for x in pick(function (n: i32): i32 { return n * 2; }) { s = s + x; } return s; }`, 6},
}

// TestNativeFnArgInCond exercises the three condition positions on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeFnArgInCond(t *testing.T) {
	for _, tc := range fnArgInCondCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeFnArgInCondArm64 is the arm64 leg (CI-gated; runs under qemu).
func TestNativeFnArgInCondArm64(t *testing.T) {
	for _, tc := range fnArgInCondCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFnArgInCondIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pins routing to "ir", and oracle-checks the exit code.
func TestSelfHostFnArgInCondIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range fnArgInCondCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFnArgInCondIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFnArgInCondIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-arg-in-cond wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fnArgInCondCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
			watFile := filepath.Join(dir, "fn_arg_in_cond_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("fn-arg-in-cond wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
