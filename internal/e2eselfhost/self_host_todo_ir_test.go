package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The `todo;` / `todo("msg");` stub statement desugars in the self-host
// parser (mirroring the native parser) to
// `while (true) { eprint("todo[: msg]"); exit(101); }` — the native
// desugar's `loop { ... }` wrapper lowers to the same while-true shape.
// The always-true loop makes the stub diverge for the checker's
// missing-return analysis, so a bare `todo;` can stand in for a whole
// non-void function body; a REACHED todo aborts with exit 101. The
// constructs the desugar uses (`while`, string `+`, `eprint`, `exit`)
// all already lower on the self-host IR path, so there is no dedicated
// codegen — same contract as the assert desugar this mirrors (#4416).
var todoIRCases = []struct {
	name string
	main string
	want int
}{
	// The stubbed function isn't called on the live path → normal return.
	// This is also the E052 shape: `helper` is a non-void function whose
	// body is nothing but `todo;`, and the self-host checker must accept it.
	{"stub-not-taken", `function helper(): i32 { todo; }
function main(): i32 { if (false) { return helper(); } return 9; }`, 9},
	// A reached whole-function stub aborts with 101.
	{"stub-reached", `function helper(): i32 { todo("not written"); }
function main(): i32 { return helper(); }`, 101},
	// Bare reached form.
	{"bare-reached", `function main(): i32 { todo; }`, 101},
	// `todo` stays usable as an ordinary identifier.
	{"identifier", `function main(): i32 { var todo: i32 = 5; todo = todo + 1; return todo + 2; }`, 8},
}

// TestSelfHostTodoIRX86_64 routes each case through the self-hosted x86-64
// IR driver and runs the produced binary, oracle-checking its exit code.
func TestSelfHostTodoIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range todoIRCases {
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

// TestSelfHostTodoIRWasm runs the same cases through the wasm IR backend.
// `wasmtime run` uses the CLI convention (main's return → process exit
// code, `exit(101)` → process exit code), so both the unreached and
// aborting cases are observable here.
func TestSelfHostTodoIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host todo wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range todoIRCases {
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
			watFile := filepath.Join(dir, "todo_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("todo wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
