package e2eselfhost

import (
	"os/exec"
	"testing"
)

// ifExprBinaryWidthIRCases pin the return-type tag an if-expression gets when
// its branch value is ARITHMETIC rather than a bare literal.
//
// `if` in value position desugars to a zero-param IIFE, and if_expr_rt computes
// the tag that IIFE lambda carries. It classified strings, bools, floats, wide
// literals and nested if/match expressions — but not a binary, which fell to the
// catch-all "i32". So
//
//	var v: u64 = (if (false) { 107u64 / 42u64 } else { 900u64 >> 2u64 });
//
// labelled the lambda i32 while its body computed u64. The tag being non-empty
// then stops infer_ret_types_module revisiting the lambda, so nothing
// downstream can recover the width and the module bails off the IR path:
// "did not lower: `return` of binary `/`".
//
// This is the same defect #6272 / #6276 fixed for the literal and nested-IIFE
// branch kinds; a binary was the remaining unclassified one. The fix takes
// wider_rt of the two operands — the same combination parse_if_chain already
// applies ACROSS branches — so a width on either side survives, and a
// comparison short-circuits to bool whatever its operands are.
//
// Both repro cases were checked to BAIL on the parent commit; both controls
// already compiled and must keep compiling. The controls are what isolate the
// WIDTH as the variable rather than "arithmetic in a branch".
var ifExprBinaryWidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	// u64 arithmetic in both arms — the shape reduced from the corpus.
	{"u64-binary-arms", `function main(): i32 {
    var v: u64 = (if (false) { (107u64 / 42u64) } else { (900u64 >> 2u64) });
    return (v as i32) & 63i32;
}`, 33},
	// i64, and with the width carried by a literal too large for i32 rather
	// than by a suffix alone.
	{"i64-binary-arms", `function main(): i32 {
    var v: i64 = (if (true) { (5000000000i64 / 2i64) } else { (7i64 + 1i64) });
    return ((v / 1000000000i64) as i32) & 63i32;
}`, 2},
	// Control: the same shape at i32. It compiled before and must keep
	// compiling — wider_rt returns the left tag when neither side is wide, so
	// this path is unchanged.
	{"i32-binary-arms-control", `function main(): i32 {
    var v: i32 = (if (true) { (10i32 / 3i32) } else { (7i32 + 1i32) });
    return v & 63i32;
}`, 3},
	// Control: a COMPARISON branch. Its operands are i64 but the value is a
	// bool, so the comparison test must win over the operand width — reading
	// the operands here would tag the lambda i64 and break a working program.
	{"comparison-branch-control", `function main(): i32 {
    var v: boolean = (if (true) { (3i64 > 2i64) } else { false });
    if (v) { return 21i32; }
    return 1i32;
}`, 21},
}

// TestSelfHostIfExprBinaryWidthIRX86_64 drives the production x86-64 IR path and
// asserts the answer.
func TestSelfHostIfExprBinaryWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ifExprBinaryWidthIRCases {
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

// TestSelfHostIfExprBinaryWidthIRArm64 — the arm64 counterpart. The tag is
// computed in the shared parser, so arm64 picks it up unchanged.
func TestSelfHostIfExprBinaryWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 if-expr binary-width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ifExprBinaryWidthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
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
