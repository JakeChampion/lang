package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var tryOptionPayloadIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"result-option", "function f(n: i32): Result[Option[i32], i32] { return Ok(Some(n)); } function g(n: i32): Result[i32, i32] { let x: Option[i32] = f(n)?; match (x) { Some(v) => { return Ok(v); }, None => { return Ok(0); } } } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 5},
	{"nested-chain", "function inner(n: i32): Result[Option[i32], i32] { if (n > 0) { return Ok(Some(n)); } return Ok(None); } function outer(n: i32): Result[i32, i32] { let o: Option[i32] = inner(n)?; match (o) { Some(v) => { return Ok(v); }, None => { return Ok(0); } } } function main(): i32 { match (outer(9)) { Ok(v) => { return v; }, Err(_) => { return 88; } } }", 9},
	{"result-i32", "function f(n: i32): Result[i32, i32] { return Ok(n); } function g(n: i32): Result[i32, i32] { let x: i32 = f(n)?; return Ok(x); } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 5},
	{"result-string", "function f(n: i32): Result[string, i32] { return Ok(\"hi\"); } function g(n: i32): Result[i32, i32] { let x: string = f(n)?; return Ok(x.len()); } function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }", 2},
	{"option-payload", "function f(o: Option[i32]): Option[i32] { let x: i32 = o?; return Some(x + 1); } function main(): i32 { match (f(Some(6))) { Some(v) => { return v; }, None => { return 9; } } }", 7},
}

func TestSelfHostTryOptionPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tryOptionPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: did not lower through the IR (no .Lssa_ labels)", tc.name)
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

func TestSelfHostTryOptionPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 try-option-payload gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tryOptionPayloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if !strings.Contains(string(asm), ".Lssa_") {
				t.Fatalf("%s: arm64 asm has no .Lssa_ marker — module bailed to the AST path", tc.name)
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
