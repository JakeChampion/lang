package e2eselfhost

import (
	"os/exec"
	"testing"
)

// valueBlockGenericWidthIRCases pin that a value-position `if`/`match` keeps its
// width when the arms CALL something and the block sits where no annotation can
// supply it — #6468's remaining positions.
//
// The IIFE's return-type tag is guessed in the parser by `if_expr_rt`, which has
// no function table, so any call falls to its "i32" default. #6481 closed the
// `var x: i64 = …` route by reading the binding's annotation. These three
// positions have no binding to read: a struct-literal field, a call argument, an
// array element. The width is recovered after parsing instead, where the
// function table exists.
//
// `id[T](x: T): T` is the shape that makes it recoverable: an erased generic
// that returns its argument, so the result's width is the argument's. Each case
// asserts the ANSWER, since a lost width shows up as a wrong value, not a crash.
var valueBlockGenericWidthIRCases = []struct {
	name string
	src  string
	exit int
}{
	{"struct-field-position", `function id[T](x: T): T { return x; }
struct Box { n: i64, tag: i32 }
function main(): i32 {
    var s: Box = Box { n: (if (true) { id(5000000000i64) } else { id(7i64) }), tag: 1 };
    return (s.n / 1000000000i64) as i32;
}`, 5},
	{"call-argument-position", `function id[T](x: T): T { return x; }
function pick[T](c: boolean, a: T, b: T): T { if (c) { return a; } return b; }
function widen(v: i64): i64 { return v; }
function main(): i32 {
    var v: i64 = widen((if (false) { id(3i64) } else { pick(true, 6000000000i64, 4i64) }));
    return (v / 1000000000i64) as i32;
}`, 6},
	{"array-element-position", `function id[T](x: T): T { return x; }
function main(): i32 {
    var xs: i64[] = [1i64, (if (true) { id(7000000000i64) } else { id(2i64) }), 3i64];
    return (xs[1] / 1000000000i64) as i32;
}`, 7},
	// A suffixed literal PAST i32-max but inside u32-max must stay u32 rather
	// than being read as 64-bit by magnitude — the trap the literal classifiers
	// already carry a note about, and one the recovery walks straight into if it
	// reads value before suffix.
	{"u32-suffix-past-i32max-control", `function id[T](x: T): T { return x; }
function main(): i32 {
    var w: u32 = (if (true) { id(2147484197u32) } else { id(1u32) });
    return ((w / 1000000u32) as i32) & 63i32;
}`, 35},
	// Control: an arm that names its width with a bare wide literal always
	// worked, and must keep working.
	{"wide-literal-arm-control", `function main(): i32 {
    var v: i64 = (if (true) { 5000000000i64 } else { 7i64 });
    return (v / 1000000000i64) as i32;
}`, 5},
}

// TestSelfHostValueBlockGenericWidthIRX86_64 drives the production x86-64 IR path.
func TestSelfHostValueBlockGenericWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockGenericWidthIRCases {
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

// TestSelfHostValueBlockGenericWidthIRArm64 — the arm64 counterpart. The
// recovery is in the shared parser, so arm64 picks it up unchanged; running it
// is what proves that rather than assuming it.
func TestSelfHostValueBlockGenericWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 value-block generic-width gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockGenericWidthIRCases {
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
