package e2ecompiler

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
    let v0: (i32) => i32 = ((x0: i32) => 189i32);
    let w: boolean = ((851i64 / 242i64) != 763i64);
    let fs: ((i32) => i32)[] = [v0, (if (w) { v0 } else { ((x1: i32) => x1) }), v0];
    return (fs[1](40i32) + fs[0](1i32)) & 63i32;
}`, 58},
	// A lambda inside a `match` arm that is itself inside an `if` branch — two
	// levels of value-position desugar. Reduced from seed s0073.
	{"lambda-in-match-arm-in-if-branch", `enum E0 { __E0_V0, __E0_V1 }
function main(): i32 {
    let p1: E0 = __E0_V1;
    let fs: ((i32) => i32)[] = (if (false) { [((x0: i32) => 105i32)] } else { [(match (p1) { __E0_V0 => ((x4: i32) => x4), __E0_V1 => ((x5: i32) => 548i32) })] });
    return fs[0](3i32) & 63i32;
}`, 36},
	// The same mixed shape with the arms the other way round: the BOXED arm is
	// the one taken, the raw-lambda arm is dead. The uniformity rule is about
	// the binding's one ABI, so which arm runs must not matter.
	{"boxed-arm-first-raw-arm-second", `enum E0 { __E0_V0, __E0_V1 }
function main(): i32 {
    let p1: E0 = __E0_V1;
    let fs: ((i32) => i32)[] = (if (true) { [(match (p1) { __E0_V0 => ((x4: i32) => x4), __E0_V1 => ((x5: i32) => 548i32) })] } else { [((x0: i32) => 105i32)] });
    return fs[0](3i32) & 63i32;
}`, 36},
	// Three arms, two of them plain lambda arrays and the third an array holding
	// a nested value-position `if`. Two sibling arms answering "raw" is what
	// makes this different from the two-arm shapes: the gate counts boxed
	// elements across ALL arms, it does not compare a pair.
	{"three-arms-one-nested-iife-array", `enum E0 { __E0_V0, __E0_V1, __E0_V2 }
function main(): i32 {
    let p1: E0 = __E0_V2;
    let fs: ((i32) => i32)[] = (match (p1) {
        __E0_V0 => [((x0: i32) => 105i32)],
        __E0_V1 => [((x1: i32) => 7i32)],
        __E0_V2 => [(if (true) { ((x2: i32) => 548i32) } else { ((x3: i32) => x3) })]
    });
    return fs[0](3i32) & 63i32;
}`, 36},
	// Control: the lambdas sit in an array the `if` YIELDS, not nested inside
	// another expression in the branch. That already lowered — the branch value
	// is the array itself, so the existing walk reached it.
	{"if-yields-lambda-array-control", `function main(): i32 {
    let fs: ((i32) => i32)[] = (if (true) { [((x0: i32) => 105i32), ((x1: i32) => 859i32)] } else { [((x3: i32) => (947i32 - x3))] });
    return (fs[0](7i32) + fs[1](7i32)) & 63i32;
}`, 4},
	// One arm yields the array literal directly, the other yields a NESTED
	// value-position if whose own arms do. The arm walk reached a nested
	// if/match written as a statement but not one written as an expression, so
	// the "every arm is an array literal" gate answered no and the capturing
	// lambda in the first arm never got its box. Reduced from seed s0017.
	{"nested-iife-arm-yields-lambda-array", `enum Status { Active, Inactive }
function main(): i32 {
    let v1: Status = Active;
    let fs: ((i32) => i32)[] = (match (v1) {
        Active => [((x0: i32) => (match (v1) { Active => (x0 + 1i32), Inactive => 5i32 }))],
        Inactive => (if (true) { [((x1: i32) => 1i32)] } else { [((x2: i32) => 2i32)] })
    });
    return fs[0](40i32) & 63i32;
}`, 41},
	// A lambda passed as a call argument inside a value-position match's GUARD.
	// A match on an enum keeps its guards as guards (a literal scrutinee's
	// if-chain makes each one an if condition), and the IIFE body walk skipped
	// guards, so the lifted `__lam_N` reached the lowering unboxed. Reduced from
	// nightly differential seed 82671.
	{"lambda-in-enum-match-guard", `function f(g: (i32) => i32): i32 { return g(1i32); }
function main(): i32 {
    let v: Result[i32, i32] = Ok(819i32);
    return (match (v) { Ok(a) when (f(((x: i32) => 274i32)) > 3i32) => 42i32, Ok(b) => 7i32, Err(e) => 9i32 });
}`, 42},
	// The same, with a lambda capturing a local and the arm's payload binding.
	{"capturing-lambda-in-enum-match-guard", `function f(g: (i32) => i32): i32 { return g(1i32); }
function main(): i32 {
    let k: i32 = 40i32;
    let o: Option[i32] = Some(3i32);
    return (match (o) { Some(n) when (f(((x: i32) => x + k + n)) == 44i32) => n + 20i32, Some(m) => 1i32, None => 2i32 });
}`, 23},
	// The guard is FALSE, so the answer depends on the rewritten guard running.
	{"false-lambda-guard-falls-through", `function f(g: (i32) => i32): i32 { return g(1i32); }
function main(): i32 {
    let k: i32 = 40i32;
    let o: Option[i32] = Some(3i32);
    return (match (o) { Some(n) when (f(((x: i32) => x + k + n)) > 1000i32) => 42i32, Some(m) => m + 4i32, None => 2i32 });
}`, 7},
	// A literal payload folds into the guard beside the written one.
	{"lambda-guard-on-literal-payload", `function f(g: (i32) => i32): i32 { return g(1i32); }
function main(): i32 {
    let v: Result[i32, i32] = Ok(0i32);
    return (match (v) { Ok(0i32) when (f(((x: i32) => 274i32)) > 3i32) => 42i32, Ok(a) => 7i32, Err(e) => 9i32 });
}`, 42},
	// The guard's lambda inside an array literal argument, the shape seed 82671
	// generated.
	{"lambda-array-in-enum-match-guard", `function f(gs: ((i32) => i32)[]): i32 { return gs[0](1i32); }
function main(): i32 {
    let v: Result[i32, i32] = Ok(819i32);
    return (match (v) { Ok(a) when (f([((x: i32) => 274i32)]) > 3i32) => 42i32, Ok(b) => 7i32, Err(e) => 9i32 });
}`, 42},
	// A value-position `if` of lambdas handed through a generic passthrough to
	// a fn-typed parameter. The passthrough boxing took only a bare lambda or
	// fn name, so the arms stayed raw. Reduced from nightly seed 78232.
	{"lambda-iife-through-passthrough", `function id[T](x: T): T { return x; }
function g(p: (i32) => i32): i32 { return p(3i32); }
function main(): i32 {
    let b: boolean = true;
    return g(id((if (b) { ((x: i32) => x + 30i32) } else { ((x: i32) => 206i32) })));
}`, 33},
	// The `if`'s CONDITION holds lambdas too. Walking the condition changed the
	// body, and that returned before the arm values were boxed. Reduced from
	// nightly seed 76638.
	{"lambda-iife-with-lambda-in-condition", `function g(p1: ((i32) => i32)[]): boolean { return p1.len() > 1i32; }
function main(): i32 {
    let fs: ((i32) => i32)[] = [(if (g([((x: i32) => x), ((x: i32) => 4i32)])) { ((x: i32) => x + 40i32) } else { ((x: i32) => 136i32) })];
    return fs[0](3i32);
}`, 43},
	// A call in a fn-typed argument slot whose own argument is a lambda: the
	// slot's boxing returned a non-passthrough call untouched, so its arguments
	// were never walked. Reduced from nightly seed 76233.
	{"lambda-in-call-at-fn-slot", `function lf(n: u8, g: (i32) => i32): (i32) => i32 { return g; }
function main(): i32 {
    let h: (i32) => i32 = lf(1u8, lf(7u8, ((x: i32) => x + 2i32)));
    return h(4i32);
}`, 6},
	// A local function called from a value-position `if` that also yields a
	// lambda. The `if` is hoisted with the local function as a capture, which
	// made it a value and stopped its direct-call lift, and nothing boxed it.
	// Reduced from nightly seed 78835.
	{"local-fn-called-in-hoisted-iife", `function main(): i32 {
    function lf(k: i32): (i32) => i32 { return ((x: i32) => x + k); }
    let b: boolean = true;
    let h: (i32) => i32 = (if (b) { lf(3i32) } else { ((x: i32) => 216i32) });
    return h(1i32);
}`, 4},
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
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
    let b: boolean = true;
    let w: i32 = (if (b) { (if (true) { 7i32 } else { 2i32 }) } else { (if (b) { 9i32 } else { 3i32 }) });
    return w & 63i32;
}`

// TestSelfHostNestedIifeStaysWholeX86_64 asserts the gate STRUCTURALLY: the
// emitted asm must carry no hoisted lambda, because the desugar stayed whole.
// The answer is asserted too, so a module that lowers to the wrong thing still
// fails.
//
// Asserting the hoist rather than "the module lowers" is what keeps this meaningful
// when an unrelated bail moves: whether some other defect refuses this shape
// varies, but whether the gate held is a property of the gate alone.
func TestSelfHostNestedIifeStaysWholeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
