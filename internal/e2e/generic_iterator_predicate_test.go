package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Predicate-based iterator adapters — `any` / `all` / `find` — that take a
// `(T) => boolean` closure through a function-typed parameter. These work on
// every NATIVE backend (interp / x86-64 / wasm / arm64) AND on the self-host IR
// path (TestSelfHostGenericPredicateAdaptersIR* below).
//
// The earlier diagnosis here — "a closure whose RETURN type is `boolean`, called
// indirectly through a function-typed parameter, miscompiles" — was WRONG. The
// real #2686-tail bug was that the self-host IR lift pass
// (lift_inline_closures_stmts) walked only var-init / return / expr-stmt /
// assignment and the nested BODIES of if/while/for — never a statement's
// CONDITION or iterated expression. Every case here calls the adapter in such a
// position (`if (any(…))` / `if (all(…))` / `match (find(…))`), so the lambda
// argument was never env-boxed while the callee's fn-param — marked a closure
// local — unpacked an env box from the bare fn pointer and crashed. The "boolean
// return" / "A≠T" framings were symptoms of which cases happened to sit in a
// condition, not a codegen bug. With if/while/for conditions now walked, `any` /
// `all` lower on the IR path; `find` (called in a `match` scrutinee, a position
// the lift pass still leaves to the AST emitter) runs correctly via the AST
// fallback — both produce the oracle result on every backend.
var predicateAdapterPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function any[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return true; } cur = t.1; }, None => { go = false; }, } } return false; }
function all[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (!pred(t.0)) { return false; } cur = t.1; }, None => { go = false; }, } } return true; }
function find[T, I: Iterator[T]](it: I, pred: (T) => boolean): Option[T] { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return Some(t.0); } cur = t.1; }, None => { go = false; }, } } return None; }
`

var predicateAdapterCases = []struct {
	name string
	main string
	want int
}{
	// any: 0..5 contains 3 → true → 5.
	{"any-hit", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x == 3; })) { return 5; } return 0; }`, 5},
	// any: 0..5 has no value > 9 → false → 9.
	{"any-miss", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x > 9; })) { return 0; } return 9; }`, 9},
	// all: every value in 0..5 is < 10 → true → 6.
	{"all-true", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x < 10; })) { return 6; } return 0; }`, 6},
	// all: not every value is even → false → 8.
	{"all-false", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, function (x: i32): boolean { return x % 2 == 0; })) { return 0; } return 8; }`, 8},
	// find: first even ≥ 2 in 0..9 → Some(2).
	{"find-some", `function main(): i32 { match (find(RangeIter { cur: 0, end: 9 }, function (x: i32): boolean { return x >= 2 && x % 2 == 0; })) { Some(v) => { return v; }, None => { return 99; } } }`, 2},
	// find: no match → None → 7.
	{"find-none", `function main(): i32 { match (find(RangeIter { cur: 0, end: 3 }, function (x: i32): boolean { return x > 100; })) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

// TestNativeGenericPredicateAdapters pins any/all/find on the native interp /
// x86-64 / wasm backends. See predicateAdapterPrelude for the self-host caveat.
func TestNativeGenericPredicateAdapters(t *testing.T) {
	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := predicateAdapterPrelude + tc.main + "\n"
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(prog), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, prog); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeGenericPredicateAdaptersArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeGenericPredicateAdaptersArm64(t *testing.T) {
	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := predicateAdapterPrelude + tc.main + "\n"
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(prog), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericPredicateAdaptersIRX86_64 drives the predicate adapters
// through the self-hosted x86-64 compiler and oracle-checks the exit code. Both
// the IR-lowered cases (`any` / `all`, called in an `if` condition the lift pass
// now walks) and the AST-fallback case (`find`, called in a `match` scrutinee)
// must produce the right answer end-to-end — so this asserts behaviour, not the
// routing tag (which differs case to case by design).
func TestSelfHostGenericPredicateAdaptersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(predicateAdapterPrelude + tc.main + "\n")
			asm := runCapture(t, gcc, runner, driverBin, prog)
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
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericPredicateAdaptersIRWasm is the wasm leg.
func TestSelfHostGenericPredicateAdaptersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host predicate-adapter wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(predicateAdapterPrelude + tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(prog)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "predicate_adapter_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("predicate-adapter wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
