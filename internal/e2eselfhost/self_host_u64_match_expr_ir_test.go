package e2eselfhost

import (
	"os/exec"
	"testing"
)

// --- u64 match-EXPRESSION (#6647) --------------------------------------------
//
// A match-expression desugars to an immediately-invoked value block whose
// payload binding is admitted by iife_payload_field_bindable. That gate is a
// width whitelist — i32, u32 (#6400), then a wide arm for i64 / f64 — and u64
// was in none of them, so `match (o) { Some(v) => v, … }` on a u64 payload took
// the whole MODULE off the IR path while the i64 spelling of the identical
// program lowered. The statement form already lowered either way: the StmtMatch
// binder carries the width on the slot and never consults this gate.
//
// u64 is i64-WIDE, so it normalises to i64 before the wide arm rather than being
// OR'd into it — the arm forwards the width into payload-arith shape tests that
// compare against "i64", and threading the raw "u64" there would widen the
// admission while silently stopping those tests matching.
//
// unsigned-div is the row that would catch that kind of mistake: `(2^64-1) / 2`
// is 2^63-1 unsigned and 0 signed, so a normalisation that leaked into the
// PRODUCING expression's signedness shows up as a wrong answer rather than as a
// quieter bail. The i64 rows are the controls that must keep lowering.
//
// Every case runs under FERN_STRICT_IR (#6602), which is what makes these
// mutation checks at all: the AST answers are correct, so an exit-code-only
// assertion passes on the unfixed compiler.
var u64MatchExprCases = []struct {
	name string
	src  string
	exit int
}{
	// The bare form — the one whose message names the value block directly.
	{"bare-return", `function f(a: u64, b: u64): u64 { return (match (a *? b) { Some(v) => v, None => 7u64 }); }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 30},
	// As an operand: the operator is incidental (`+`, `*`, `/`, `%` all bailed
	// identically), and either side of the binary reaches the same gate.
	{"operand-add", `function f(o: Option[u64], b: u64): u64 { return (match (o) { Some(v) => v, None => 7u64 }) + b; }
function main(): i32 { return (f(Some(10u64), 3u64) as i32) & 63; }`, 13},
	{"operand-div", `function f(a: u64, b: u64): u64 { return (match (a *? b) { Some(v) => v, None => 7u64 }) / b; }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 10},
	{"rhs-operand", `function f(a: u64, b: u64): u64 { return a / (match (a *? b) { Some(v) => v, None => 7u64 }); }
function main(): i32 { return (f(30u64, 3u64) as i32) & 63; }`, 0},
	// Binding position rather than return position.
	{"var-bound", `function f(a: u64, b: u64): u64 { var c: u64 = (match (a *? b) { Some(v) => v, None => 7u64 }); return c + b; }
function main(): i32 { return (f(10u64, 3u64) as i32) & 63; }`, 33},
	// 2^64-1 / 2 — 2^63-1 unsigned (&63 == 63), 0 signed.
	{"unsigned-div", `function f(o: Option[u64]): u64 { return (match (o) { Some(v) => v, None => 7u64 }) / 2u64; }
function main(): i32 { return (f(Some(18446744073709551615u64)) as i32) & 63; }`, 63},
	// Controls: the i64 spelling lowered before this change and must keep doing so.
	{"i64-control", `function f(a: i64, b: i64): i64 { return (match (a *? b) { Some(v) => v, None => 7i64 }) / b; }
function main(): i32 { return (f(10i64, 3i64) as i32) & 63; }`, 10},
	{"i32-control", `function f(a: i32, b: i32): i32 { return (match (a *? b) { Some(v) => v, None => 7i32 }) / b; }
function main(): i32 { return f(10, 3) & 63; }`, 10},
}

// TestSelfHostU64MatchExprIRX86_64 — the x86-64 IR path.
func TestSelfHostU64MatchExprIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range u64MatchExprCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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

// TestSelfHostU64MatchExprIRArm64 — the arm64 IR path lowers from the same
// irlower predicate, so the gate is shared; the emit is not.
func TestSelfHostU64MatchExprIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range u64MatchExprCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
