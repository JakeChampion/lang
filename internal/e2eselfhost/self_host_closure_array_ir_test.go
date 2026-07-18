package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureArrayIRCases pin arrays of CAPTURING closures — built, indexed, and
// CALLED through the array — on the self-host IR path (x86-64 + wasm). The
// existing call_on_call coverage has a single NON-capturing named-function value
// in a one-element array called at a constant index (`fs[0](4)`); these cases
// exercise the distinct shape: capturing lambdas (`() => n`) stored in a
// multi-element ARRAY-LITERAL `(() => i32)[]`, called via a VARIABLE index in a
// loop. That drives closure-env boxing (the captured `n` / `k`) AND dynamic
// dispatch through an array element (the indirect call target comes from a
// runtime array read), which the constant-index non-capturing case does not.
// All of it already lowers, so no compiler change — an observability pin against
// a regression to the AST fallback.
//
// The `.append`-built form (`fns = fns.append(() => n)`) is now covered too
// (#3556): an EMPTY closure-array literal `var fns: (() => i32)[] = []` leaves
// the slot a generic array (no closure elements to infer from at the decl), so
// the later `fns[i]()` dispatched a closure box as a plain fn pointer and
// segfaulted while `all_eligible` wrongly admitted it. The fix marks the slot a
// closure array at the FIRST closure `.append` (when the appended value is a
// `__mkclo$…` env box / closure-returning call / closure local), so the indexed
// call dispatches env-first. A bare NAMED-function value (`[f]` / `append(f)`) is
// a plain fn POINTER, not a closure box: the `namedfn-*` cases below flow through
// the is_fnarr slot flag (#3574) — each element lowers to const_func and
// dispatches via PLAIN call_indirect. The fn[]-literal `[f, g]` is handled at the
// StmtVar binding; the `append(f)` form reads is_fnarr at the append site so a
// bare 0-arg fn name emits const_func instead of const-calling f (which
// segfaulted). They share this harness because the routing-pin + run is identical.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; every result stays <= 120 (the wasm exit-code clamp,
// #2908).
var closureArrayIRCases = []struct {
	name string
	main string
	want int
}{
	// three capturing closures, summed via a loop-variable index: 3+4+6 = 13.
	{"loop-sum", `function main(): i32 { var n = 3; var fns: (() => i32)[] = [() => n, () => n + 1, () => n * 2]; var s = 0; var i = 0; while (i < 3) { s = s + fns[i](); i = i + 1; } return s; }`, 13},
	// constant-index call of a capturing closure: () => n + 1 with n = 3.
	{"index-const", `function main(): i32 { var n = 3; var fns: (() => i32)[] = [() => n, () => n + 1]; return fns[1](); }`, 4},
	// one-arg capturing closures: (a)=>a+k and (a)=>a*k with k=2 -> 7 + 10 = 17.
	{"two-arg-cap", `function main(): i32 { var k = 2; var fns: ((i32) => i32)[] = [(a: i32) => a + k, (a: i32) => a * k]; return fns[0](5) + fns[1](5); }`, 17},
	// single capturing closure in a one-element array.
	{"single-cap", `function main(): i32 { var n = 9; var fns: (() => i32)[] = [() => n]; return fns[0](); }`, 9},
	// #3556: append a capturing closure to an EMPTY `(() => i32)[]`, then call it.
	{"append-empty", `function main(): i32 { var n = 4; var fns: (() => i32)[] = []; fns = fns.append(() => n); return fns[0](); }`, 4},
	// append two closures, call both: 10 + 20 = 30.
	{"append-two", `function main(): i32 { var a = 10; var b = 20; var fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); return fns[0]() + fns[1](); }`, 30},
	// append onto a NON-empty literal, then call the appended element.
	{"append-after-literal", `function main(): i32 { var n = 4; var fns: (() => i32)[] = [() => n]; fns = fns.append(() => n + 1); return fns[1](); }`, 5},
	// append three, then sum them via a `for` loop over the array: 1+2+3 = 6.
	{"append-loop", `function main(): i32 { var a = 1; var b = 2; var c = 3; var fns: (() => i32)[] = []; fns = fns.append(() => a); fns = fns.append(() => b); fns = fns.append(() => c); var s = 0; for f in fns { s = s + f(); } return s; }`, 6},
	// #3574: a bare NAMED-fn value appended to an empty fn-pointer array (is_fnarr),
	// then called — previously const-called f and segfaulted (exit -1).
	{"namedfn-append-empty", `function f(): i32 { return 7; } function main(): i32 { var fns: (() => i32)[] = []; fns = fns.append(f); return fns[0](); }`, 7},
	// append two named fns, call both: 7 + 5 = 12.
	{"namedfn-append-two", `function f(): i32 { return 7; } function g(): i32 { return 5; } function main(): i32 { var fns: (() => i32)[] = []; fns = fns.append(f); fns = fns.append(g); return fns[0]() + fns[1](); }`, 12},
	// append a named fn onto a NON-empty named-fn literal (the literal marks
	// is_fnarr at its StmtVar binding; the append reads it), then call it.
	{"namedfn-append-after-literal", `function f(): i32 { return 7; } function g(): i32 { return 5; } function main(): i32 { var fns: (() => i32)[] = [f]; fns = fns.append(g); return fns[1](); }`, 5},
	// append three named fns, sum via a `for` loop: 1 + 2 + 4 = 7.
	{"namedfn-append-loop", `function a(): i32 { return 1; } function b(): i32 { return 2; } function c(): i32 { return 4; } function main(): i32 { var fns: (() => i32)[] = []; fns = fns.append(a); fns = fns.append(b); fns = fns.append(c); var s = 0; for f in fns { s = s + f(); } return s; }`, 7},
	// #5071: a MIXED closure array (capturing + non-capturing lambda in ONE
	// literal). The capturing element makes the array env-first-dispatched, so
	// the non-capturing element must be boxed into a `$wrap` env trampoline
	// too — otherwise its bare fn pointer is deref'd as a box → SIGSEGV. Both
	// element orderings, plus loop / append / fn-arg dispatch.
	// capturing first, dispatch the non-capturing elem: (5)*(5) = 25.
	{"mixed-cap-then-noncap", `function main(): i32 { var base = 2; var fs: ((i32) => i32)[] = [(x: i32) => x + base, (x: i32) => x * x]; return fs[1](5); }`, 25},
	// non-capturing first, dispatch the capturing elem: 5 + 2 = 7.
	{"mixed-noncap-then-cap", `function main(): i32 { var base = 2; var fs: ((i32) => i32)[] = [(x: i32) => x * x, (x: i32) => x + base]; return fs[1](5); }`, 7},
	// mixed array summed over a variable-index loop: (5+2) + (5*5) = 32.
	{"mixed-loop", `function main(): i32 { var base = 2; var fs: ((i32) => i32)[] = [(x: i32) => x + base, (x: i32) => x * x]; var s = 0; var i = 0; while (i < fs.len()) { s = s + fs[i](5); i = i + 1; } return s; }`, 32},
	// append a non-capturing lambda onto a NON-empty capturing-lambda literal,
	// then dispatch the appended element: 5 * 5 = 25.
	{"mixed-append-noncap", `function main(): i32 { var base = 2; var fs: ((i32) => i32)[] = [(x: i32) => x + base]; fs = fs.append((y: i32) => y * y); return fs[1](5); }`, 25},
	// #5071 follow-up: a MIXED array of a NAMED function value and a capturing
	// lambda. The capturing element env-first-dispatches the array, so the
	// named-fn element must be boxed into a `$wrap` env trampoline too — else
	// its bare fn pointer is deref'd as a box (or a box is called bare) →
	// SIGSEGV. Both orderings + loop dispatch.
	// named fn first, dispatch the capturing lambda: 5 + 3 = 8.
	{"mixed-namedfn-then-cap", `function inc(x: i32): i32 { return x + 100; } function main(): i32 { var a = 3; var fs: ((i32) => i32)[] = [inc, (x: i32) => x + a]; return fs[1](5); }`, 8},
	// capturing lambda first, dispatch the named fn: 5 + 100 = 105.
	{"mixed-cap-then-namedfn", `function inc(x: i32): i32 { return x + 100; } function main(): i32 { var a = 3; var fs: ((i32) => i32)[] = [(x: i32) => x + a, inc]; return fs[1](5); }`, 105},
	// named-fn + capturing lambda summed over a loop: (5+10) + (5+3) = 23.
	{"mixed-namedfn-loop", `function inc(x: i32): i32 { return x + 10; } function main(): i32 { var a = 3; var fs: ((i32) => i32)[] = [inc, (x: i32) => x + a]; var s = 0; var i = 0; while (i < fs.len()) { s = s + fs[i](5); i = i + 1; } return s; }`, 23},
	// #5109: a closure-array PARAM dispatched inside the callee is marked
	// is_closurearr (→ env-first) only when a whole-program scan proves every
	// call site passes a closure array. That scan must classify calls at EVERY
	// expression position, not just statement-top-level ones — otherwise a call
	// buried in a match scrutinee / if-condition / call-argument / binary
	// operand is uncounted, the proof fails, the param stays unmarked, and
	// `fs[i](args)` dispatches PLAIN → bare-calls the element box → SIGSEGV.
	// callee called from an ENUM (Option) match scrutinee: fs[0](4)=14 → Some → 14.
	{"param-enum-match-scrutinee", `function get(fs: ((i32) => i32)[], v: i32): Option[i32] { var f = fs[0]; return Some(f(v)); } function main(): i32 { var k = 10; var fs: ((i32) => i32)[] = [(x: i32) => x + k]; match (get(fs, 4)) { Some(v) => { return v; }, None => { return 0; } } }`, 14},
	// callee called from a SCALAR/literal match scrutinee (desugars to an if
	// on a binary compare — the call sits in the condition): 4+10=14 → arm 14.
	{"param-scalar-match-scrutinee", `function get(fs: ((i32) => i32)[], v: i32): i32 { var f = fs[0]; return f(v); } function main(): i32 { var k = 10; var fs: ((i32) => i32)[] = [(x: i32) => x + k]; match (get(fs, 4)) { 14 => { return 100; }, _ => { return 99; } } }`, 100},
	// callee called in an IF-CONDITION: get(fs,4)==14 → true → 100.
	{"param-if-condition", `function get(fs: ((i32) => i32)[], v: i32): i32 { var f = fs[0]; return f(v); } function main(): i32 { var k = 10; var fs: ((i32) => i32)[] = [(x: i32) => x + k]; if (get(fs, 4) == 14) { return 100; } return 99; }`, 100},
	// callee called as an ARGUMENT to another call: id(get(fs,4)) → 14.
	{"param-call-argument", `function id(x: i32): i32 { return x; } function get(fs: ((i32) => i32)[], v: i32): i32 { var f = fs[0]; return f(v); } function main(): i32 { var k = 10; var fs: ((i32) => i32)[] = [(x: i32) => x + k]; return id(get(fs, 4)); }`, 14},
	// regression: a BARE-fn-pointer array param (named fns, is_fnarr) must STAY
	// plain-dispatched even when the callee is called from a match scrutinee —
	// the scan must NOT over-eagerly mark it is_closurearr. inc(5)=6 → arm 100.
	{"param-namedfn-plain-in-match", `function inc(x: i32): i32 { return x + 1; } function apply(fs: ((i32) => i32)[], v: i32): i32 { var f = fs[0]; return f(v); } function main(): i32 { var fs: ((i32) => i32)[] = [inc]; match (apply(fs, 5)) { 6 => { return 100; }, _ => { return 99; } } }`, 100},
}

// TestSelfHostClosureArrayIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostClosureArrayIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range closureArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostClosureArrayIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostClosureArrayIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-array wasm IR e2e")
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

	for _, tc := range closureArrayIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "closure_array_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("closure-array wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
