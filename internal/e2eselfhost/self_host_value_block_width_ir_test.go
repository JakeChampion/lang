package e2eselfhost

import (
	"os/exec"
	"testing"
)

// valueBlockWidthIRCases pin that a value-position `if`/`match` bound to a
// 64-bit binding keeps its width — #6468.
//
// The desugared IIFE's return-type tag comes from a syntax-only classifier
// (if_expr_rt), which has no function table, so an arm that CALLS anything and
// an arm that is an enum payload binding both fall to "i32". The tag then
// contradicted the body, and being non-empty it also stopped the return-type
// inference revisiting the lambda, so the module bailed off the IR path
// ("did not lower: `return` of ident `w`"). The binding's annotation is the
// authority the classifier lacks.
//
// The exit code is asserted, not just that the module lowered: a compile-only
// assertion passes on a silent miscompile.
var valueBlockWidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The reduced repro: a payload arm and a call arm, neither of which names a
	// width. The None arm is taken.
	{"i64-call-arm", `function widen(v: i64): i64 { return v; }
function main(): i32 {
    var x: i64 = (match ((1i64) /? (0i64)) { Some(w) => w, None => widen(5000000000i64) });
    return (x / 1000000000i64) as i32;
}`, 5},
	// The same shape with the PAYLOAD arm taken, so the width has to survive on
	// the binding that the classifier could not read either.
	{"i64-payload-arm", `function widen(v: i64): i64 { return v; }
function main(): i32 {
    var x: i64 = (match ((9000000000i64) /? (2i64)) { Some(w) => w, None => widen(0i64) });
    return (x / 1000000000i64) as i32;
}`, 4},
	// u64 takes the same route as i64 — the annotation is read, not assumed.
	{"u64-call-arm", `function ident_u(v: u64): u64 { return v; }
function main(): i32 {
    var x: u64 = (match ((1u64) /? (0u64)) { Some(w) => w, None => ident_u(9000000000u64) });
    return (x / 1000000000u64) as i32;
}`, 9},
	// Control: a WIDE LITERAL arm already named the width, so this shape lowered
	// before the annotation was consulted and must keep doing so.
	{"wide-literal-arm-control", `function main(): i32 {
    var x: i64 = (match ((1i64) /? (0i64)) { Some(w) => w, None => 5000000000i64 });
    return (x / 1000000000i64) as i32;
}`, 5},
}

// TestSelfHostValueBlockWidthIRX86_64 drives the production x86-64 IR path.
func TestSelfHostValueBlockWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockWidthIRCases {
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

// TestSelfHostValueBlockWidthIRArm64 — the arm64 counterpart. The tag is set in
// the shared parser, so arm64 picks it up unchanged; running it is what proves
// that rather than assuming it.
func TestSelfHostValueBlockWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 value-block width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockWidthIRCases {
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
