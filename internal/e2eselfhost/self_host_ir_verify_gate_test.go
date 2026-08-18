package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #6639 slice 5: the IR verifiers now run on the COMPILE path.
//
// Slices 1-3 wrote the passes; nothing ran them outside a test driver.
// irverify_run.fern builds op streams by hand and irlower_run.fern sweeps the
// conformance corpus — neither is the compiler, so the module with the most
// lowering in it, the self-host compiler's own ~1000 functions, went through
// every build unchecked. examples/self_host/irverifygate.fern closes that: all
// three backends call it once per function they are about to emit, so any
// compile run under FERN_IR_VERIFY=1 names the malformed op where it was
// produced instead of handing it to a backend that turns it into a SIGSEGV or
// a module wasmtime refuses to validate.
//
// The tests below split along the only line that matters for a verifier:
// whether it stays SILENT on valid IR (the property that decides if the flag
// is usable at all) and whether it actually FIRES on invalid IR (the property
// that decides if it is worth anything).

// gateProgs exercise the op classes a stack-discipline or frame bug would most
// likely hide in — closures and their environment slots, string and array
// builtins with their varied arities, maps, enum matches, nested control flow.
// A backend that never sees these does not tell you much about the gate.
var gateProgs = []struct {
	name string
	src  string
}{
	{"scalars", `function chain(a: i32): i32 {
    var b: i32 = a * 3;
    var c: i32 = b + a;
    return c;
}
function main(): i32 { return chain(7); }`},
	{"loops-and-branches", `function loopy(k: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < k) {
        if (i % 2 == 0) { acc = acc + i; } else { acc = acc - 1; }
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return loopy(10); }`},
	{"strings-and-arrays", `function work(s: string, xs: i32[]): i32 {
    var sl: string = s[1:4];
    var joined: string = s + sl;
    var w: i32[] = xs.with(1, 9);
    var sum: i32 = 0;
    for x in w { sum = sum + x; }
    return joined.len() + sum + s[0];
}
function main(): i32 { return work("hello", [4, 5, 6]); }`},
	{"closures", `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var add5: (i32) => i32 = makeAdder(5);
    var inc: (i32) => i32 = function(x: i32): i32 { return x + 1; };
    return apply(add5, 30) + inc(2);
}`},
	{"enum-match", `enum Shape { Dot, Line(i32), Box(i32, i32) }
function area(s: Shape): i32 {
    match (s) {
        Shape.Dot => { return 0; },
        Shape.Line(n) => { return n; },
        Shape.Box(w, h) => { return w * h; }
    }
}
function main(): i32 { return area(Shape.Box(3, 4)) + area(Shape.Line(2)); }`},
	// Receiver methods are the arity check's off-by-one case: the call site
	// pushes the receiver as an argument and the declaration does not list it,
	// so an index that counted only `params` would report every method call in
	// every program the compiler builds.
	{"methods", `struct Acc { n: i32 }
pub function (a: Acc) bump(by: i32): i32 { return a.n + by; }
pub function (a: Acc) zero(): i32 { return a.n; }
function main(): i32 {
    var a: Acc = Acc { n: 5 };
    var s: string = "abc";
    return a.bump(2) + a.zero() + s.len();
}`},
	{"structs", `struct P { x: i32, y: i32 }
function shift(p: P, d: i32): P { return P { ...p, x: p.x + d }; }
function main(): i32 {
    var p: P = P { x: 3, y: 4 };
    var q: P = shift(p, 5);
    var ps: P[] = [p, q];
    return ps[0].x + ps[1].x + q.y;
}`},
}

// runGate compiles src with the driver under FERN_IR_VERIFY=1 or =0, and
// returns stdout, stderr and the exit code. It does not fail the test on a
// non-zero exit: every caller here is asserting something about that code.
func runGate(t *testing.T, runner []string, bin string, args []string, src string, verify bool) (string, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		a := append([]string{}, runner[1:]...)
		a = append(a, bin)
		a = append(a, args...)
		cmd = exec.Command(runner[0], a...)
	}
	cmd.Stdin = strings.NewReader(src)
	// Strip any ambient value first: a duplicate key resolves to the FIRST
	// occurrence, so appending alone would leave an outer setting in force and
	// the "flag off" half of every case below would silently test nothing.
	//
	// Both halves are then set EXPLICITLY. The gate is on by default, so an
	// unset variable is the on state — leaving it out would make "off" mean
	// "on" and every byte-identity comparison below compare a run with itself.
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_IR_VERIFY=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if verify {
		cmd.Env = append(cmd.Env, "FERN_IR_VERIFY=1")
	} else {
		cmd.Env = append(cmd.Env, "FERN_IR_VERIFY=0")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("driver did not exit normally")
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// gateSilentSweep is the shared body of the three backend legs: every program
// compiles clean under the flag, and emits EXACTLY what it emits without it.
//
// The byte-identity half is not decoration. A gate that perturbed codegen would
// mean the flag changes the program under test, and every diagnosis made with
// it turned on would be about a different compiler than the one that failed.
func gateSilentSweep(t *testing.T, runner []string, bin string, args []string) {
	t.Helper()
	for _, tc := range gateProgs {
		t.Run(tc.name, func(t *testing.T) {
			off, offErr, offCode := runGate(t, runner, bin, args, tc.src, false)
			if offCode != 0 {
				t.Fatalf("compile without the flag exited %d:\n%s", offCode, offErr)
			}
			if len(off) == 0 {
				t.Fatal("compile without the flag emitted 0 bytes")
			}
			on, onErr, onCode := runGate(t, runner, bin, args, tc.src, true)
			if onCode != 0 {
				t.Fatalf("FERN_IR_VERIFY=1 refused valid IR (exit %d) — the gate reports on code the compiler accepts:\n%s", onCode, onErr)
			}
			if on != off {
				t.Errorf("FERN_IR_VERIFY=1 changed the emitted code (%d bytes vs %d) — the gate must observe, not perturb", len(on), len(off))
			}
		})
	}
}

// TestSelfHostIRVerifyGateSilentX86_64 is the x86-64 leg, through asm_ir's
// emit_function_via_ir_pre — the call site both x86 entry points share.
func TestSelfHostIRVerifyGateSilentX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	gateSilentSweep(t, runner, bin, nil)
}

// TestSelfHostIRVerifyGateSilentArm64 is the arm64 leg, through
// asm_arm64_ir's own emit_function_via_ir.
//
// It needs no arm64 tooling: the driver is the x86-hosted one and `-target
// arm64-linux` only changes which backend emits. What is under test is the
// gate's verdict on the arm64 op stream, not whether the result runs — the
// arm64 e2e suites already cover that.
func TestSelfHostIRVerifyGateSilentArm64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	gateSilentSweep(t, runner, bin, []string{"-target", "arm64-linux"})
}

// TestSelfHostIRVerifyGateSilentWasm is the wasm leg, through wasm_ir's
// emit_function_ir — the backend with the most to gain, since malformed IR
// there fails module validation with a message about the module rather than
// about the op that caused it.
func TestSelfHostIRVerifyGateSilentWasm(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	gateSilentSweep(t, runner, bin, []string{"-ir"})
}

// TestSelfHostIRVerifyGateRefuses is the other direction, and the one that
// makes the sweeps above mean something: a gate that returned "clean"
// unconditionally would pass every one of them.
//
// It cannot be driven from a backend — the compiler does not emit malformed IR
// on demand — so irverify_run's `-refuse` mode calls verify_or_refuse directly
// with a stream whose local index is outside the frame. That exercises the real
// function: the FERN_IR_VERIFY read, the decision to act on a non-empty
// verdict, the message, and the exit code the backends inherit.
func TestSelfHostIRVerifyGateRefuses(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	// Opted out: inert. The gate runs on every function of every build, so a
	// working FERN_IR_VERIFY=0 is as much of the contract as the refusal is —
	// it is the escape hatch when the gate itself is the suspect.
	out, _, code := runGate(t, nil, bin, []string{"-refuse"}, "", false)
	if code != 0 {
		t.Errorf("-refuse under FERN_IR_VERIFY=0 exited %d, want 0 — the opt-out must make the gate inert", code)
	}
	if !strings.Contains(out, "irverifygate: inert") {
		t.Errorf("-refuse under FERN_IR_VERIFY=0 printed %q, want the inert marker", out)
	}

	// Flag set: refuses, names the function, and says what is wrong with it.
	_, stderr, code := runGate(t, nil, bin, []string{"-refuse"}, "", true)
	if code != 4 {
		t.Errorf("-refuse under FERN_IR_VERIFY=1 exited %d, want 4\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"FERN_IR_VERIFY",
		"probe lowered to malformed IR",
		"local index 5 is outside the frame",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal message missing %q:\n%s", want, stderr)
		}
	}
}

// TestSelfHostIRVerifyGateChecks pins the gate's own unit assertions, which run
// inside irverify_run's default mode (case ids 170-177).
//
// The marker is asserted because the exit code alone would not notice
// gate_checks being dropped from main: a call that is never made cannot fail.
func TestSelfHostIRVerifyGateChecks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irverify_run.fern", "irverify_run")

	out, stderr, code := runGate(t, nil, bin, nil, "", false)
	if code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing case's id\n%s", code, stderr)
	}
	if want := "irverifygate: compile-path gate agrees"; !strings.Contains(out, want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostIRVerifyGateWholeCompiler is the case the gate exists for: the
// self-host compiler's own sources, every module, through the per-module
// bootstrap driver with the flag on.
//
// This is ~1000 functions of the hardest code the lowerer sees, and until now
// no verifier had ever looked at any of it — the corpus sweeps run over
// conformance/cases, which are single-module fixtures. A refusal here is a real
// lowering bug in the compiler, which is why the failure message says to read
// it as one rather than as a verifier false positive.
func TestSelfHostIRVerifyGateWholeCompiler(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-compiler compile is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "modload_run")

	// asm_ir_run.fern imports the whole x86 pipeline, so its closure is the
	// compiler: lexer, parser, ir, irlower, asmcore, asm_ir, asm_arm64_ir. The
	// modload project stages every one of those already; only the entry itself
	// has to be added.
	copySelfHostFiles(t, dir, "asm_ir_run.fern")
	entry := filepath.Join(dir, "asm_ir_run.fern")

	cmd := exec.Command(bin, entry)
	cmd.Dir = dir
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_IR_VERIFY=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "FERN_IR_VERIFY=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("modload driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("compiling the self-host compiler under FERN_IR_VERIFY=1 exited %d.\n"+
			"Exit 4 is the gate: read the message as a lowering bug in the named function, not as a verifier false positive.\n%s",
			code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatalf("modload driver emitted 0 bytes\n%s", stderr.String())
	}
}
