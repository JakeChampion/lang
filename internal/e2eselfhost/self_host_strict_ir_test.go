package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FERN_STRICT_IR (#5646) turns the self-host compiler's IR-to-AST bail into a
// hard error instead of a silent fall-through.
//
// The fallback is only SAFE when the AST emitter can express what the IR path
// declined. When it can't, the fallback emits wrong code and nothing notices
// until a differential test disagrees at a runtime exit code, far from the
// cause. #5642 is the worked example: `match (a +? b)` had no `ExprBinary` case
// in `lower_match`'s scrutinee-type recovery, so the enclosing function bailed
// to an AST emitter with no checked-operator lowering at all. That surfaced as
// 46 failing subtests whose symptoms read like several unrelated bugs — wrong
// match arm taken, payload read as zero, SIGABRT — none of which were
// checked-arithmetic bugs.
//
// These tests are the tripwire that would have caught it at the bail. Two
// halves, and both are load-bearing:
//
//   - strictIRCorpus asserts NO refusal across constructs the IR path is
//     supposed to cover. A newly-unlowerable construct fails here, naming the
//     function, instead of miscompiling.
//   - TestSelfHostStrictIRRefusesBail asserts a real bail DOES refuse, so a
//     green corpus means the tripwire is armed rather than inert.
//
// The corpus also self-certifies its own routing: a program that fell back
// would exit 3 under the flag, so "strict run succeeded" IS "lowered on the IR
// path" — no separate path-probe assertion needed.
//
// The flag is checked in asm_ir.fern, which both backends' eligibility runs
// through (wasm_ir's `wasm_eligible` calls `asm_ir.eligible_core`), so the
// x86-64 and wasm legs cover the same per-function gate.
//
// Every `want` must be in [0, 126): the wasm leg exits through WASI, which
// rejects anything above that with `exit with invalid exit status`, whereas an
// ELF exit code is simply taken mod 256. A case that returns 160 therefore
// passes on x86-64 and traps on wasm, which reads like a backend miscompile.
var strictIRCorpus = []struct {
	name string
	src  string
	want int
}{
	// The #5642 shape itself: checked operators in a match scrutinee, the
	// construct whose missing recovery case motivated the issue. Both arms are
	// exercised — f(100, 3) fits u8, f(250, 10) overflows.
	{"checked-operators", `
function f(a: u8, b: u8): i32 {
    match (a +? b) { Some(v) => { return v as i32; }, None => { return 99; } }
}
function g(a: i32, b: i32): i32 {
    match (a *? b) { Some(v) => { return v; }, None => { return 7; } }
}
function main(): i32 { return f(100, 3) + g(2, 3) - f(250, 10); }
`, 10},
	// Closures with captures, held in an array and dispatched through a
	// fn-typed param.
	{"closures", `
function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 {
    var n: i32 = 5;
    var add: (i32) => i32 = function (x: i32): i32 { return x + n; };
    var dbl: (i32) => i32 = function (x: i32): i32 { return x * 2; };
    var fs: ((i32) => i32)[] = [add, dbl];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < fs.len()) { t = t + apply(fs[i], 3); i = i + 1; }
    return t;
}
`, 14},
	// Enum payloads, a guarded arm, and an exhaustive match.
	{"enum-match-guard", `
enum Shape { Circle(i32), Rect(i32, i32), Empty }
function area(s: Shape): i32 {
    match (s) {
        Circle(r) when r > 10 => { return 999; },
        Circle(r) => { return 3 * r * r; },
        Rect(w, h) => { return w * h; },
        Empty => { return 0; }
    }
}
function main(): i32 { return area(Circle(2)) + area(Rect(3, 4)) + area(Empty); }
`, 24},
	// Heap traffic: a struct array grown by append, with string fields read
	// back after construction.
	{"struct-array-strings", `
struct P { name: string, n: i32 }
function label(i: i32): string {
    if (i % 2 == 0) { return "ab"; }
    return "xyz";
}
function build(n: i32): P[] {
    var out: P[] = [];
    var i: i32 = 0;
    while (i < n) { out = out.append(P { name: label(i) + "!", n: i }); i = i + 1; }
    return out;
}
function main(): i32 {
    var ps: P[] = build(5);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < ps.len()) { if (ps[i].name.len() == 3) { t = t + ps[i].n + 1; } i = i + 1; }
    return t;
}
`, 9},
	// Generics, tuples, and the `?` operator — the other consuming position
	// whose scrutinee-type recovery #5642 had to fix alongside lower_match's.
	{"generics-tuples-try", `
function pair[K, V](k: K, v: V): (K, V) { return (k, v); }
function first(t: (i32, string)): i32 { return t.0; }
function parse(s: string): Result[i32, string] {
    if (s == "ok") { return Ok(1); }
    return Err("bad");
}
function chain(s: string): Result[i32, string] {
    var v: i32 = parse(s)?;
    return Ok(v + 41);
}
function main(): i32 {
    var t: (i32, string) = pair(1, "x");
    match (chain("ok")) { Ok(v) => { return v + first(t); }, Err(_) => { return 0; } }
}
`, 43},
	// A match whose scrutinee is a call through a capture-free / capturing
	// closure LOCAL returning Option: the lambda must lift to a hoisted __lam_N
	// so the call resolves and the scrutinee's Option type recovers. Before the
	// StmtMatch arm in irlower's subst_fcall_stmts, the leftover `f` reference in
	// `match (f())` blocked the binding lift, so the lambda fell to the inline
	// escaping-closure path (const_func(<fn>$clo)) and bailed the module to AST
	// (#3457 slice 3). Under the flag these must route IR (no exit-3 bail).
	{"match-closure-local-opt", `
function main(): i32 {
    var f: () => Option[i32] = () => Some(7);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-capturing-closure-local-opt", `
function main(): i32 {
    var n: i32 = 7;
    var f: () => Option[i32] = () => Some(n);
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	// A match whose scrutinee calls an ANNOTATED fn-typed local bound to a named
	// Option/Result-returning fn (`var f: () => Option[i32] = g; match (f())`):
	// the binding seeds its return type (mark_closure_opt_ret, gated on the
	// fn-type annotation) so the payload recovers and the module routes IR. The
	// unannotated `var f = g` form is deliberately NOT covered — its `f()` call
	// miscompiles on the IR path, so it stays on the AST fallback.
	{"match-fnlocal-named-opt", `
function g(): Option[i32] { return Some(7); }
function main(): i32 {
    var f: () => Option[i32] = g;
    match (f()) { Some(v) => { return v; }, None => { return 0; } }
}
`, 7},
	{"match-fnlocal-named-result", `
function g(): Result[i32, i32] { return Ok(5); }
function main(): i32 {
    var f: () => Result[i32, i32] = g;
    match (f()) { Ok(v) => { return v; }, Err(_) => { return 9; } }
}
`, 5},
	// `?` whose success payload is itself a bracketed generic
	// (`Result[Option[i32], E]`) — the last per-function shape lower_try
	// declined (#3457 endgame). The payload box is pointer-shaped, read
	// through the same op_opt_payload as a struct/enum, and the `var x:
	// Option[i32] = f(n)?` binding types the slot from its annotation, so
	// the following `match (x)` recovers both arms.
	{"try-generic-payload", `
function f(n: i32): Result[Option[i32], i32] { return Ok(Some(n)); }
function g(n: i32): Result[i32, i32] {
    var x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Ok(v); }, None => { return Ok(0); } }
}
function main(): i32 { match (g(5)) { Ok(v) => { return v; }, Err(_) => { return 9; } } }
`, 5},
	// The same shape on a bare Option (`Option[Option[i32]]`), plus a
	// None-payload leg so the inner enum's other variant is exercised too.
	{"try-generic-payload-option", `
function f(n: i32): Option[Option[i32]] { if (n > 3) { return Some(Some(n)); } return Some(None); }
function g(n: i32): Option[i32] {
    var x: Option[i32] = f(n)?;
    match (x) { Some(v) => { return Some(v + 1); }, None => { return Some(50); } }
}
function main(): i32 {
    var a: i32 = 0;
    match (g(7)) { Some(v) => { a = v; }, None => { a = 99; } }
    match (g(1)) { Some(v) => { a = a + v; }, None => { a = a + 99; } }
    return a;
}
`, 58},
	// A `?`-chain whose bound generic payload is itself unwrapped by a second
	// `?`: the payload slot must survive being fed back into the try path.
	{"try-generic-payload-chain", `
function inner(n: i32): Result[Result[i32, i32], i32] { if (n > 0) { return Ok(Ok(n)); } return Ok(Err(3)); }
function outer(n: i32): Result[i32, i32] {
    var o: Result[i32, i32] = inner(n)?;
    var v: i32 = o?;
    return Ok(v * 2);
}
function main(): i32 { match (outer(9)) { Ok(v) => { return v; }, Err(_) => { return 88; } } }
`, 18},
}

// runDriver runs a self-host driver over `src`, optionally with FERN_STRICT_IR
// set, and returns stdout, stderr and the exit code.
func runDriver(t *testing.T, runner []string, bin string, src []byte, strict bool, args ...string) ([]byte, string, int) {
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
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Strip any ambient FERN_STRICT_IR first: the whole package can be run under
	// it as a probe (see docs/SELFHOST-AST-RETIREMENT.md), and inheriting it would
	// make the "unset" leg strict too, silently voiding the inertness assertion.
	cmd.Env = []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "FERN_STRICT_IR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if strict {
		cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1")
	}
	_ = cmd.Run()
	return stdout.Bytes(), stderr.String(), cmd.ProcessState.ExitCode()
}

// overBudgetProgram is a module past the 512-function merged-bundle budget
// (#3425) — the one bail site that is deterministically reachable from a valid
// program, and so the only way to prove the tripwire fires.
func overBudgetProgram() []byte {
	var b strings.Builder
	for i := 0; i < 513; i++ {
		fmt.Fprintf(&b, "function zf%d(): i32 { return %d; }\n", i, i%7)
	}
	b.WriteString("function main(): i32 { return zf0() + zf1(); }\n")
	return []byte(b.String())
}

func strictIRDriver(t *testing.T) (string, []string, string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	return gcc, runner, buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
}

// TestSelfHostStrictIRX86_64 asserts the corpus lowers with no bail under
// FERN_STRICT_IR, that the flag is otherwise inert (byte-identical asm), and
// that each program still runs to its expected exit code.
func TestSelfHostStrictIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			off, _, offCode := runDriver(t, runner, driverBin, src, false)
			if offCode != 0 || len(off) == 0 {
				t.Fatalf("driver (unset) exited %d with %d bytes", offCode, len(off))
			}
			on, stderr, onCode := runDriver(t, runner, driverBin, src, true)
			if strings.Contains(stderr, "FERN_STRICT_IR:") {
				t.Fatalf("%s bailed to the AST emitter under FERN_STRICT_IR:\n%s", tc.name, stderr)
			}
			if onCode != 0 {
				t.Fatalf("driver (FERN_STRICT_IR=1) exited %d\n%s", onCode, stderr)
			}
			if !bytes.Equal(off, on) {
				t.Fatalf("%s: FERN_STRICT_IR changed the emitted asm (%d vs %d bytes); the flag must only affect the bail path", tc.name, len(off), len(on))
			}
			progBin := buildBin(t, gcc, dir, "strict_"+tc.name, string(on))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrictIRRefusesBail is the teeth: a program that genuinely bails
// must refuse under the flag and fall back silently without it. Without this,
// a green corpus is consistent with the flag doing nothing at all.
func TestSelfHostStrictIRRefusesBail(t *testing.T) {
	_, runner, driverBin := strictIRDriver(t)
	src := overBudgetProgram()

	off, _, offCode := runDriver(t, runner, driverBin, src, false)
	if offCode != 0 || len(off) == 0 {
		t.Fatalf("unset: driver exited %d with %d bytes, want a silent AST fallback", offCode, len(off))
	}
	on, stderr, onCode := runDriver(t, runner, driverBin, src, true)
	if onCode != 3 {
		t.Fatalf("FERN_STRICT_IR=1: driver exited %d with %d bytes, want a refusal (3)\n%s", onCode, len(on), stderr)
	}
	if !strings.Contains(stderr, "FERN_STRICT_IR:") || !strings.Contains(stderr, "512-function") {
		t.Errorf("refusal did not name the bail:\n%s", stderr)
	}
}

// TestSelfHostStrictIRWasm runs the corpus through the wasm IR driver. The
// eligibility gate is shared (wasm_eligible calls asm_ir.eligible_core), so the
// same per-function bail is covered on both backends.
func TestSelfHostStrictIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host strict-IR wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strictIRCorpus {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			wat, stderr, code := runDriver(t, runner, driverBin, src, true, "-ir")
			if strings.Contains(stderr, "FERN_STRICT_IR:") {
				t.Fatalf("%s bailed to the AST emitter under FERN_STRICT_IR:\n%s", tc.name, stderr)
			}
			if code != 0 || len(wat) == 0 {
				t.Fatalf("driver (FERN_STRICT_IR=1) exited %d with %d bytes\n%s", code, len(wat), stderr)
			}
			watFile := filepath.Join(dir, "strict_ir_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("strict-IR wasm %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
