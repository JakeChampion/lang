package e2eselfhost

import (
	"os/exec"
	"testing"
)

// valueBlockElemWidthCases pin the WIDTH of a value block sitting in an
// ARRAY-LITERAL element position.
//
// `if` / `match` in value position desugar to an IIFE whose return tag
// if_expr_rt guesses from the arms' syntax, and a CALL arm is unguessable at
// parse time — there is no function table there — so it falls to i32. With a
// literal i64 arm the across-arms wider_rt combination recovers the width
// (#6246); with a call arm both arms read i32, the IIFE is labelled i32, and
// the i64 `return` inside it bails the module (#6593).
//
// stamp_value_block_ret is the annotation authority that outranks the guess,
// and it applied only to a scalar binding. A `T[]` annotation names the ELEMENT
// type, so an element gets the same treatment.
//
// literal-arm-unchanged and scalar-binding-unchanged are the two routes that
// already recovered the width; they must keep doing so.
//
// NOTE on what these do and do not prove. The driver does not set
// FERN_STRICT_IR, so on the parent commit the bail falls THROUGH to the AST
// emitter, which computes every case here correctly — so all five pass on both
// sides and none of them is a mutation check. They pin the ANSWERS against a
// future miscompile. The evidence that the shape stays on the IR path is the
// strict-IR corpus census in the PR (3 seeds off the bail pile, 0 divergences);
// no asm-label witness separates the two, because a bailing module still emits
// `.Lir_*` labels for the functions that did lower.
var valueBlockElemWidthCases = []struct {
	name string
	src  string
	exit int
}{
	// Reduced from fernsmith seed 161, then rewritten to READ the element — the
	// reduced seed never reads what it builds.
	{"call-arm-in-i64-array-elem", `function id[T](x: T): T { return x; } function main(): i32 { var xs: i64[] = [(match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => id((if (true) { 1099511628488i64 } else { 5i64 })) })]; return ((xs[0i32] as i32) & 63i32); }`, 7},
	// The same shape with an if-expression rather than a checked-arith match.
	{"call-arm-ifexpr-i64-array-elem", `function id[T](x: T): T { return x; } function main(): i32 { var xs: i64[] = [(if (true) { id(1099511628488i64) } else { 5i64 })]; return ((xs[0i32] as i32) & 63i32); }`, 8},
	{"literal-arm-unchanged", `function main(): i32 { var xs: i64[] = [(match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => 0i64 })]; return ((xs[0i32] as i32) & 63i32); }`, 7},
	{"scalar-binding-unchanged", `function main(): i32 { var w: i64 = (match ((366i64 ^ 456i64) +? 673i64) { Some(v) => v, None => 0i64 }); return ((w as i32) & 63i32); }`, 7},
	// An i32[] element must NOT be widened: the stamp only ever widens to
	// i64 / u64, so a narrow annotation leaves the guess alone.
	{"i32-array-elem-unchanged", `function main(): i32 { var xs: i32[] = [(if (true) { 7i32 } else { 5i32 })]; return (xs[0i32] & 63i32); }`, 7},
}

// TestSelfHostValueBlockElemWidthIRX86_64 — the x86-64 IR path.
func TestSelfHostValueBlockElemWidthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockElemWidthCases {
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

// TestSelfHostValueBlockElemWidthIRArm64 — the arm64 IR path.
func TestSelfHostValueBlockElemWidthIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range valueBlockElemWidthCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
