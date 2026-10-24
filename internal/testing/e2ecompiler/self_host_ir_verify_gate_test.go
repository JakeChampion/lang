package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

var gateProgs = []struct {
	name string
	src  string
}{
	{"scalars", `function chain(a: i32): i32 {
    let b: i32 = a * 3;
    let c: i32 = b + a;
    return c;
}
function main(): i32 { return chain(7); }`},
	{"loops-and-branches", `function loopy(k: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        if (i % 2 == 0) { acc = acc + i; } else { acc = acc - 1; }
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return loopy(10); }`},
	{"strings-and-arrays", `function work(s: string, xs: i32[]): i32 {
    let sl: str = slice_unchecked(s, 1, 4);
    let joined: string = s + sl;
    let w: i32[] = xs.with(1, 9);
    let sum: i32 = 0;
    for x in w { sum = sum + x; }
    return joined.len() + sum + s[0] as i32;
}
function main(): i32 { return work("hello", [4, 5, 6]); }`},
	{"closures", `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let add5: (i32) => i32 = makeAdder(5);
    let inc: (i32) => i32 = (x: i32): i32 => { return x + 1; };
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
	{"methods", `struct Acc { n: i32 }
pub function (a: Acc) bump(by: i32): i32 { return a.n + by; }
pub function (a: Acc) zero(): i32 { return a.n; }
function main(): i32 {
    let a: Acc = Acc { n: 5 };
    let s: string = "abc";
    return a.bump(2) + a.zero() + s.len();
}`},
	{"structs", `struct P { x: i32, y: i32 }
function shift(p: P, d: i32): P { return P { ...p, x: p.x + d }; }
function main(): i32 {
    let p: P = P { x: 3, y: 4 };
    let q: P = shift(p, 5);
    let ps: P[] = [p, q];
    return ps[0].x + ps[1].x + q.y;
}`},
	{"own-spread-in-returning-arm", `struct Line { mode: i32, cursor: i32, buf: u8[] }
function put(own l: Line, n: i32): Line {
    if (l.mode == 2) {
        return Line { ...l, buf: l.buf.append(32 as u8) };
    }
    return Line { ...l, cursor: n };
}
function main(): i32 {
    let l: Line = Line { mode: 2, cursor: 0, buf: [] };
    l = put(l, 3);
    l = put(Line { ...l, mode: 1 }, 7);
    return l.cursor + l.buf.len();
}`},
}

func runGate(t *testing.T, bin string, args []string, flag string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = e2eharness.ChildEnv()
	if flag != "" {
		cmd.Env = childEnv(flag)
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

func gateSilentSweep(t *testing.T, target string) {
	t.Helper()
	cli := newStrictCLI(t)
	for _, tc := range gateProgs {
		t.Run(tc.name, func(t *testing.T) {
			off := cli.emit(t, target, tc.src, "FERN_IR_VERIFY=0")
			if len(off) == 0 {
				t.Fatal("compile without the flag emitted 0 bytes")
			}
			on, onErr, err := cli.tryEmit(t, target, tc.src, "FERN_IR_VERIFY=1")
			if err != nil {
				t.Fatalf("FERN_IR_VERIFY=1 refused valid IR (%v) — the gate reports on code the compiler accepts:\n%s", err, onErr)
			}
			if on != off {
				t.Errorf("FERN_IR_VERIFY=1 changed the emitted code (%d bytes vs %d) — the gate must observe, not perturb", len(on), len(off))
			}
		})
	}
}

func TestSelfHostIRVerifyGateSilentX86_64(t *testing.T) {
	gateSilentSweep(t, "x86-64-linux")
}

func TestSelfHostIRVerifyGateSilentArm64(t *testing.T) {
	gateSilentSweep(t, "arm64-linux")
}

func TestSelfHostIRVerifyGateSilentWasm(t *testing.T) {
	gateSilentSweep(t, "wasm32-wasi")
}

func TestSelfHostIRVerifyGateRefuses(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

	out, _, code := runGate(t, bin, []string{"-refuse"}, "")
	if code != 0 || !strings.Contains(out, "irverifygate: inert") {
		t.Errorf("-refuse with FERN_IR_VERIFY unset exited %d and printed %q, want 0 and the inert marker", code, out)
	}

	out, _, code = runGate(t, bin, []string{"-refuse"}, "FERN_IR_VERIFY=0")
	if code != 0 {
		t.Errorf("-refuse under FERN_IR_VERIFY=0 exited %d, want 0 — the opt-out must make the gate inert", code)
	}
	if !strings.Contains(out, "irverifygate: inert") {
		t.Errorf("-refuse under FERN_IR_VERIFY=0 printed %q, want the inert marker", out)
	}

	_, stderr, code := runGate(t, bin, []string{"-refuse"}, "FERN_IR_VERIFY=1")
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

func TestSelfHostIRVerifyGateChecks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irverify_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/irverify_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/irverify_run.fern", "irverify_run")

	out, stderr, code := runGate(t, bin, nil, "FERN_IR_VERIFY=0")
	if code != 0 {
		t.Fatalf("irverify_run exit code = %d, want 0 — that code is the failing case's id\n%s", code, stderr)
	}
	if want := "irverifygate: compile-path gate agrees"; !strings.Contains(out, want) {
		t.Errorf("irverify_run stdout = %q, want it to contain %q", out, want)
	}
}

func TestSelfHostIRVerifyGateWholeCompiler(t *testing.T) {
	if testing.Short() {
		t.Skip("whole-compiler compile is slow; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProjectTyped(t)
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "modload_run")

	copySelfHostFiles(t, dir, "drivers/asm_ir_run.fern")
	entry := filepath.Join(dir, "drivers/asm_ir_run.fern")

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
