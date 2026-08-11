package e2eselfhost

import (
	"os/exec"
	"testing"
)

// liftAggregateCallIRCases pin a fn-typed local CALLED from inside an aggregate
// literal — #6256, the largest group of the `<fn>$clo not defined` cluster.
//
// try_lift_binding rewrites the binding's call sites to its hoisted `__lam_N`
// and declines the lift if the name still appears afterwards. The rewrite walks
// call / binary / unary / index / field-access / lambda, so a call reached only
// through an array, tuple or struct literal was never substituted: the name
// survived, the lift declined, and the lambda fell to the escaping-closure path,
// which names a `<fn>$clo` that only a bare trailing `return <lambda>` creates.
//
// One case per literal form, plus the base-spread variant, since each is a
// separate arm. All were checked to BAIL on the parent commit.
var liftAggregateCallIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The minimal repro: the call is an array ELEMENT.
	{"call-in-array-literal", `function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var v3: i32[] = [v0(2i32)];
    return v3[0i32] & 63i32;
}`, 4},
	// The array is itself a call ARGUMENT — the shape the corpus seeds carry.
	{"call-in-array-call-arg", `function take(a: i32[], b: i32[]): i32[] { return a; }
function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var v3: i32[] = take([474i32], [v0(2i32)]);
    return v3[0i32] & 63i32;
}`, 26},
	// A tuple element.
	{"call-in-tuple-literal", `function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var t: (i32, i32) = (v0(2i32), 1i32);
    return (t.0 + t.1) & 63i32;
}`, 5},
	// A struct field value.
	{"call-in-struct-literal", `struct Box { n: i32, m: i32 }
function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var b: Box = Box { n: v0(3i32), m: 1i32 };
    return (b.n + b.m) & 63i32;
}`, 10},
	// A struct literal with a BASE spread — the base is a separate expression
	// slot from the field values and is walked separately.
	{"call-in-struct-literal-with-base", `struct Box { n: i32, m: i32 }
function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var base: Box = Box { n: 1i32, m: 2i32 };
    var b: Box = Box { ...base, n: v0(3i32) };
    return (b.n + b.m) & 63i32;
}`, 11},
	// Control: the same call hoisted to a temp before the literal. That already
	// compiled — the substitution reached it through the plain `var` init — so
	// it isolates the aggregate literal as the variable rather than the call.
	{"hoisted-to-temp-control", `function main(): i32 {
    var v0: (i32) => i32 = ((x: i32) => (x * x));
    var t: i32 = v0(2i32);
    var v3: i32[] = [t];
    return v3[0i32] & 63i32;
}`, 4},
}

// TestSelfHostLiftAggregateCallIRX86_64 drives the production x86-64 IR path and
// asserts the answer.
func TestSelfHostLiftAggregateCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range liftAggregateCallIRCases {
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

// TestSelfHostLiftAggregateCallIRArm64 — the arm64 counterpart. The lift is
// shared, so arm64 refused identically; running it proves the fix reaches both.
func TestSelfHostLiftAggregateCallIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 aggregate-call lift gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range liftAggregateCallIRCases {
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
