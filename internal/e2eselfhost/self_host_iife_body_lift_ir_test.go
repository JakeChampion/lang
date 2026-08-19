package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// iifeBodyLiftIRCases pin that a lambda nested inside a value-position
// `if`/`match` reaches the lift — #6256, the `<fn>$clo not defined` cluster. An
// unlifted one stays an AST-only closure and the IR path cannot name it.
//
// The exit code is asserted, not just that the module lowered: a compile-only
// assertion passes on a silent miscompile.
var iifeBodyLiftIRCases = []struct {
	name string
	src  string
	exit int
}{
	// A lambda written inside an `if` BRANCH, in an array of function values.
	// Reduced from seed s0002.
	{"lambda-in-if-branch", `function main(): i32 {
    var v0: (i32) => i32 = ((x0: i32) => 189i32);
    var w: boolean = ((851i64 / 242i64) != 763i64);
    var fs: ((i32) => i32)[] = [v0, (if (w) { v0 } else { ((x1: i32) => x1) }), v0];
    return (fs[1](40i32) + fs[0](1i32)) & 63i32;
}`, 58},
	// A lambda inside a `match` arm that is itself inside an `if` branch — two
	// levels of value-position desugar. Reduced from seed s0073.
	{"lambda-in-match-arm-in-if-branch", `enum E0 { __E0_V0, __E0_V1 }
function main(): i32 {
    var p1: E0 = __E0_V1;
    var fs: ((i32) => i32)[] = (if (false) { [((x0: i32) => 105i32)] } else { [(match (p1) { __E0_V0 => ((x4: i32) => x4), __E0_V1 => ((x5: i32) => 548i32) })] });
    return fs[0](3i32) & 63i32;
}`, 36},
	// Control: the lambdas sit in an array the `if` YIELDS, not nested inside
	// another expression in the branch. That already lowered — the branch value
	// is the array itself, so the existing walk reached it.
	{"if-yields-lambda-array-control", `function main(): i32 {
    var fs: ((i32) => i32)[] = (if (true) { [((x0: i32) => 105i32), ((x1: i32) => 859i32)] } else { [((x3: i32) => (947i32 - x3))] });
    return (fs[0](7i32) + fs[1](7i32)) & 63i32;
}`, 4},
	// One arm yields the array literal directly, the other yields a NESTED
	// value-position if whose own arms do. The arm walk reached a nested
	// if/match written as a statement but not one written as an expression, so
	// the "every arm is an array literal" gate answered no and the capturing
	// lambda in the first arm never got its box. Reduced from seed s0017.
	{"nested-iife-arm-yields-lambda-array", `enum Status { Active, Inactive }
function main(): i32 {
    var v1: Status = Active;
    var fs: ((i32) => i32)[] = (match (v1) {
        Active => [((x0: i32) => (match (v1) { Active => (x0 + 1i32), Inactive => 5i32 }))],
        Inactive => (if (true) { [((x1: i32) => 1i32)] } else { [((x2: i32) => 2i32)] })
    });
    return fs[0](40i32) & 63i32;
}`, 41},
}

// TestSelfHostIifeBodyLiftIRX86_64 drives the production x86-64 IR path and
// asserts the ANSWER, not just that the module lowered.
//
// runCaptureStrictIR rather than runCapture: an unlifted arm lambda still
// reaches the right answer through the per-function bail, so the exit code
// alone cannot tell the two routes apart (#6602).
func TestSelfHostIifeBodyLiftIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeBodyLiftIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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

// nestedIifeGateSrc nests a value-position `if` inside another one's branch.
// lift_call_callee's in-IIFE gate is what stops the inner one being hoisted to a
// top-level `__lam_N`, which would split one desugar across the IR and AST paths.
const nestedIifeGateSrc = `function main(): i32 {
    var b: boolean = true;
    var w: i32 = (if (b) { (if (true) { 7i32 } else { 2i32 }) } else { (if (b) { 9i32 } else { 3i32 }) });
    return w & 63i32;
}`

// TestSelfHostNestedIifeStaysWholeX86_64 asserts the gate STRUCTURALLY: the
// emitted asm must carry no hoisted lambda, because the desugar stayed whole.
// The answer is asserted too, so a module that lowers to the wrong thing still
// fails.
//
// Asserting the hoist rather than "the module lowers" is what keeps this honest
// when an unrelated bail moves: whether some other defect refuses this shape
// varies, but whether the gate held is a property of the gate alone.
func TestSelfHostNestedIifeStaysWholeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	stdout, stderr, code := runDriver(t, runner, driverBin, []byte(nestedIifeGateSrc), true, "-ir")
	if strings.Contains(stderr, "FERN_STRICT_IR:") {
		t.Fatalf("nested value-position desugar bailed:\n%s", stderr)
	}
	if code != 0 {
		t.Fatalf("driver (FERN_STRICT_IR=1) exited %d\n%s", code, stderr)
	}
	for _, ln := range strings.Split(string(stdout), "\n") {
		if strings.HasPrefix(ln, "__fn___lam_") {
			t.Errorf("the inner IIFE was hoisted to %q — the in-IIFE gate did not hold", strings.TrimSuffix(ln, ":"))
		}
	}
	progBin := buildBin(t, gcc, dir, "nested-iife-gate", string(stdout))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 7 {
		t.Errorf("nested-iife-gate exited %d, want 7", got)
	}
}

// TestSelfHostIifeBodyLiftIRArm64 — the arm64 counterpart. The lift is shared,
// so arm64 picks it up unchanged; running it is what proves that rather than
// assuming it.
func TestSelfHostIifeBodyLiftIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 IIFE-body-lift gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeBodyLiftIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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
