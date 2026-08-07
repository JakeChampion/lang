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
}

// TestSelfHostIifeBodyLiftIRX86_64 drives the production x86-64 IR path and
// asserts the ANSWER, not just that the module lowered.
func TestSelfHostIifeBodyLiftIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeBodyLiftIRCases {
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

// nestedIifeGateSrc holds a value-position `if` in both a `match` scrutinee and
// one of its arms. Hoisting the inner IIFE out of the outer one makes this
// module refuse, so it pins lift_call_callee's in-IIFE gate.
//
// Only the strict-IR verdict is asserted: this shape is mis-answered by both
// self-host backends (#6400), so an exit code here would pin the wrong number.
const nestedIifeGateSrc = `function main(): i32 {
    var b: boolean = true;
    var w: u32 = (match ((2147484129u32) -? (if (true) { 2147484571u32 } else { 250u32 })) { Some(c) => c, None => (if (b) { 5u32 } else { 687u32 }) });
    return (w as i32) & 63i32;
}`

// TestSelfHostNestedIifeStaysWholeX86_64 asserts the module still lowers on the
// IR path under FERN_STRICT_IR=1, which refuses a silent fall-through to the AST
// emitter (#5646).
func TestSelfHostNestedIifeStaysWholeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	stdout, stderr, code := runDriver(t, runner, driverBin, []byte(nestedIifeGateSrc), true, "-ir")
	if strings.Contains(stderr, "FERN_STRICT_IR:") {
		t.Fatalf("nested value-position desugar bailed to the AST emitter:\n%s", stderr)
	}
	if code != 0 {
		t.Fatalf("driver (FERN_STRICT_IR=1) exited %d\n%s", code, stderr)
	}
	if len(stdout) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
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
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range iifeBodyLiftIRCases {
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
