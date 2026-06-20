package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryOpIRCases are try-operator (`inner?`) programs that must route through the
// self-hosted x86-64 IR path and compute the hardcoded oracle exit code. `?`
// unwraps an Option[T]/Result[T,E]'s Some/Ok payload as the expression's value
// or early-returns the failure box (a fresh None for Option, the forwarded Err
// box for Result) from the enclosing function. The self-host runtime leaks, so
// the lowering skips the native path's rc/defer/errdefer cleanup. Gated to
// scalar i32/boolean payloads (string/i64/f64/composite payloads stay on the
// AST path for now); the eligibility assertion below is the proof that the
// scalar cases reach the IR path rather than the AST fallback.
var tryOpIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"opt-some", `function g(): Option[i32] { return Some(5); } function f(): Option[i32] { var x = g()?; return Some(x + 1); } function main(): i32 { match (f()) { Some(n) => { return n; }, None => { return 0; } } }`, 6},
	{"opt-none-propagates", `function g(): Option[i32] { return None; } function f(): Option[i32] { var x = g()?; return Some(x + 1); } function main(): i32 { match (f()) { Some(n) => { return n; }, None => { return 42; } } }`, 42},
	{"result-ok", `function g(): Result[i32, string] { return Ok(5); } function f(): Result[i32, string] { var x = g()?; return Ok(x + 1); } function main(): i32 { match (f()) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`, 6},
	{"result-err-forwards", `function g(): Result[i32, string] { return Err("bad"); } function f(): Result[i32, string] { var x = g()?; return Ok(x + 1); } function main(): i32 { match (f()) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }`, 3},
	{"try-local-var", `function g(): Option[i32] { return Some(8); } function f(): Option[i32] { var o = g(); var x = o?; return Some(x); } function main(): i32 { match (f()) { Some(n) => { return n; }, None => { return 0; } } }`, 8},
	{"try-in-subexpr", `function g(): Option[i32] { return Some(4); } function f(): Option[i32] { return Some(g()? + 10); } function main(): i32 { match (f()) { Some(n) => { return n; }, None => { return 0; } } }`, 14},
	{"try-boolean-payload", `function g(): Option[boolean] { return Some(true); } function f(): Option[i32] { var b = g()?; if (b) { return Some(1); } return Some(0); } function main(): i32 { match (f()) { Some(n) => { return n; }, None => { return 0; } } }`, 1},
}

// TestSelfHostTryOpX86IR gates the try-operator IR lowering on x86-64. For each
// case it asserts (1) the program routes through the "ir" path (via the
// asm_pathprobe_run driver — the same observability gate the trait / str-split
// IR-path tests use) and (2) the IR path computes the oracle exit code.
func TestSelfHostTryOpX86IR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)

	// Path-probe driver: prints "ir"/"ast" for a program on stdin.
	probeSrc, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), probeSrc, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	// asm_ir_run driver: emits asm via the IR path under -ir.
	runSrc, err := os.ReadFile("../../examples/self_host/asm_ir_run.fern")
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), runSrc, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		return string(out)
	}
	run := func(t *testing.T, asmText string) int {
		t.Helper()
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, []byte(asmText), 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s\n--- asm ---\n%s", err, out, asmText)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally")
		}
		return inner.ProcessState.ExitCode()
	}

	for _, tc := range tryOpIRCases {
		t.Run(tc.name, func(t *testing.T) {
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if route != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, route)
			}
			if got := run(t, emit(t, tc.src)); got != tc.expected {
				t.Errorf("try-op x86 IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
