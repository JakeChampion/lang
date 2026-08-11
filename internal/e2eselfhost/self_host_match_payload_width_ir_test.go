package e2eselfhost

import (
	"os/exec"
	"testing"
)

// matchPayloadWidthIRCases pin that a match-arm PAYLOAD BINDING carries its
// width into the value block that returns it — #6468's last shape.
//
// The return-type inference resolves an ident against parameters only, so
// `match (a +? b) { Some(n) => n, … }` inferred nothing for `n` and the lifted
// block kept the parser's syntax-only i32 guess. A checked operator is what
// makes the width recoverable: it yields its OPERAND type rather than an
// `Option[…]` spelling.
//
// Reduced from fernsmith seed s0161 against a DIFFERENTIAL oracle — the parent
// commit must bail and this one must both compile and agree with the
// interpreter. A bail oracle alone accepts candidates that stop bailing for
// unrelated reasons, which is how four hand-built probes for this shape went
// wrong before the reduction was automated.
var matchPayloadWidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"payload-binding-in-array-element", `function id[T](x: T): T { return x; }
function gen(): i64 {
    var fe: i64[] = [(match ((1099511628358i64) +? (200i64)) { Some(n) => n, None => id(1099511628488i64) })];
    return fe[0];
}
function main(): i32 { return ((gen() / 1000000000i64) as i32) & 63i32; }`, 11},
	// Control: the None arm taken, so the value arrives from the literal rather
	// than the binding. This lowered before.
	{"none-arm-control", `function id[T](x: T): T { return x; }
function gen(): i64 {
    var fe: i64[] = [(match ((1099511628358i64) /? (0i64)) { Some(n) => n, None => id(1099511628488i64) })];
    return fe[0];
}
function main(): i32 { return ((gen() / 1000000000i64) as i32) & 63i32; }`, 11},
}

// TestSelfHostMatchPayloadWidthIRX86_64 drives the production x86-64 IR path.
func TestSelfHostMatchPayloadWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchPayloadWidthIRCases {
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

// TestSelfHostMatchPayloadWidthIRArm64 — the arm64 counterpart. The inference is
// in the shared parser, so arm64 picks it up unchanged; running it proves that.
func TestSelfHostMatchPayloadWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 payload-width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchPayloadWidthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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
