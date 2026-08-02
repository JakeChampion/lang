package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fully-generic iterator collectors: a function generic over BOTH the iterator
// type and its element type, `f[T, I: Iterator[T]](…)`, where `T` appears only
// inside another parameter's parametrised-trait bound (`I: Iterator[T]`). On
// the NATIVE backend `T` is recovered by bound-driven inference (#2691): once
// `I` is pinned to a concrete type by the argument, the bound's trait args
// (`Iterator[T]`) unify against that type's impl's trait args (`Iterator[i32]`
// / `Iterator[boolean]`) to bind `T` — without it the checker reported
// `E040: could not infer type parameter T`. On the SELF-HOST IR path the
// unbounded `T` is erased (uniform 8-byte slots) and the function monomorphises
// on `I`, so #3558's parametrised-bound parsing already suffices; these tests
// pin that it lowers through `ir` and runs. `last[T, I: Iterator[T]](it, dflt:
// T): T` threads `T` through a parameter AND the return type, and the SAME
// generic `last` is exercised over two element types (i32 + boolean) to prove
// genuine genericity rather than i32-special-casing.
var genericCollectorCases = []struct {
	name string
	main string
	want int
}{
	// count: T appears only in the bound; collector returns i32.
	{"count-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function count[T, I: Iterator[T]](it: I): i32 { var n = 0; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { n = n + 1; cur = t.1; }, None => { go = false; }, } } return n; }
function main(): i32 { return count(RangeIter { cur: 0, end: 6 }); }`, 6},
	// last: T threaded through a parameter (dflt) AND the return type, T=i32.
	{"last-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { var acc = dflt; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return last(RangeIter { cur: 0, end: 5 }, -1); }`, 4},
	// to_array: T threaded through the RETURN type as a generic array `T[]`.
	{"to-array-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function to_array[T, I: Iterator[T]](it: I): T[] { var out: T[] = []; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; }, None => { go = false; }, } } return out; }
function main(): i32 { var xs = to_array(RangeIter { cur: 0, end: 4 }); var s = 0; for x in xs { s = s + x; } return s + xs.len(); }`, 10},
	// fold: three type params (element T, accumulator A, iterator I) + a closure
	// combiner. Here A = T = i32; sums 0..5 = 10.
	{"fold-sum", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function fold[T, A, I: Iterator[T]](it: I, init: A, f: (A, T) => A): A { var acc = init; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = f(acc, t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return fold(RangeIter { cur: 0, end: 5 }, 0, function (a: i32, x: i32): i32 { return a + x; }); }`, 10},
	// nth: index into a generic iterator → Option[T]. nth(0..9, 4) = Some(4).
	{"nth-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function nth[T, I: Iterator[T]](it: I, n: i32): Option[T] { var cur = it; var k = n; var go = true; while (go) { match (cur.next()) { Some(t) => { if (k == 0) { return Some(t.0); } k = k - 1; cur = t.1; }, None => { go = false; }, } } return None; }
function main(): i32 { match (nth(RangeIter { cur: 0, end: 9 }, 4)) { Some(v) => { return v; }, None => { return 99; } } }`, 4},
	// min / max over an i32 iterator → Option[i32]. min(3..7)=3, max(3..7)=6 → 9.
	{"min-max-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function min[I: Iterator[i32]](it: I): Option[i32] { var cur = it; var best = 0; var seen = false; var go = true; while (go) { match (cur.next()) { Some(t) => { if (!seen || t.0 < best) { best = t.0; seen = true; } cur = t.1; }, None => { go = false; }, } } if (seen) { return Some(best); } return None; }
function max[I: Iterator[i32]](it: I): Option[i32] { var cur = it; var best = 0; var seen = false; var go = true; while (go) { match (cur.next()) { Some(t) => { if (!seen || t.0 > best) { best = t.0; seen = true; } cur = t.1; }, None => { go = false; }, } } if (seen) { return Some(best); } return None; }
function main(): i32 { var lo = 0; match (min(RangeIter { cur: 3, end: 7 })) { Some(v) => { lo = v; }, None => {} } var hi = 0; match (max(RangeIter { cur: 3, end: 7 })) { Some(v) => { hi = v; }, None => {} } return lo + hi; }`, 9},
	// product / position over an i32 iterator. product(1..5)=24; position of 3 in
	// 0..9 = Some(3). 24 - 3 + 0 ... combine: product(1..5)=24, position=3 → 24-21=3.
	{"product-position", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function product[I: Iterator[i32]](it: I): i32 { var p = 1; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { p = p * t.0; cur = t.1; }, None => { go = false; }, } } return p; }
function position[I: Iterator[i32]](it: I, target: i32): Option[i32] { var cur = it; var i = 0; var go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { return Some(i); } i = i + 1; cur = t.1; }, None => { go = false; }, } } return None; }
function main(): i32 { var pr = product(RangeIter { cur: 1, end: 5 }); var po = 0; match (position(RangeIter { cur: 0, end: 9 }, 3)) { Some(v) => { po = v; }, None => {} } return pr - 7 * po; }`, 3},
	// last over a generic iterator → Option[T]. last(0..5) = Some(4).
	{"last-opt", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I): Option[T] { var cur = it; var acc: Option[T] = None; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = Some(t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { match (last(RangeIter { cur: 0, end: 5 })) { Some(v) => { return v; }, None => { return 99; } } }`, 4},
	// contains / count_value: i32 equality queries, no closure. contains(0..5,3)=true→5;
	// count_value(0..5,2)=1. Combine: 5 + 1 + 1 = 7.
	{"contains-count-value", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function contains[I: Iterator[i32]](it: I, target: i32): boolean { var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { return true; } cur = t.1; }, None => { go = false; }, } } return false; }
function count_value[I: Iterator[i32]](it: I, target: i32): i32 { var n = 0; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { n = n + 1; } cur = t.1; }, None => { go = false; }, } } return n; }
function main(): i32 { var a = 0; if (contains(RangeIter { cur: 0, end: 5 }, 3)) { a = 5; } return a + count_value(RangeIter { cur: 0, end: 5 }, 2) + 1; }`, 7},
	// the SAME generic `last` instantiated at T=boolean (different element type).
	{"last-bool", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct BoolSeq { n: i32 }
impl Iterator[boolean] for BoolSeq { function next(self: Self): Option[(boolean, Self)] { if (self.n <= 0) { return None; } return Some((true, BoolSeq { n: self.n - 1 })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { var acc = dflt; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { if (last(BoolSeq { n: 2 }, false)) { return 7; } return 0; }`, 7},
}

// foldCrossTypeProg folds an i32 iterator with a boolean ACCUMULATOR (A ≠ T)
// via a closure combiner — "are all values < 10?" over 0..4 → true → 5. The
// driver is called as `if (fold(it, init, <lambda>))`. This pins the #2686 tail
// fix: the self-host IR lift pass (lift_inline_closures_stmts) used to walk only
// var-init / return / expr-stmt / assign and the nested BODIES of if/while/for —
// NOT the condition / iterated expression — so a fn-typed call argument inside an
// `if (…)` condition was never env-boxed, while the callee's fn-param (marked a
// closure local) still unpacked a box from the bare fn pointer and crashed. The
// earlier diagnosis ("the boolean accumulator gets the closure ABI wrong") was a
// red herring: an A≠T fold bound to a `var` already worked, and an A=T fold inside
// an `if` already crashed — the discriminator was the call CONTEXT, not the types.
// Now if/while/for conditions are walked, so this lowers + runs on the self-host
// IR path (x86-64 + wasm) too — see TestSelfHostGenericFoldCrossTypeIR* below.
const foldCrossTypeProg = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function fold[T, A, I: Iterator[T]](it: I, init: A, f: (A, T) => A): A { var acc = init; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = f(acc, t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { if (fold(RangeIter { cur: 0, end: 4 }, true, function (a: boolean, x: i32): boolean { if (x < 10) { return a; } return false; })) { return 5; } return 0; }
`

// TestNativeGenericFoldCrossType pins the A≠T closure-accumulator fold on the
// native backends (interp / x86-64 / wasm). See foldCrossTypeProg for the
// #2686-tail story (fn-arg in an `if` condition now lifts on the self-host path).
func TestNativeGenericFoldCrossType(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(foldCrossTypeProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 5 {
		t.Errorf("fold-cross-type interp = %d, want 5", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 5 {
		t.Errorf("fold-cross-type x86-64 = %d, want 5", code)
	}
	if code := runWasm(t, foldCrossTypeProg); code != 5 {
		t.Errorf("fold-cross-type wasm = %d, want 5", code)
	}
}

// TestSelfHostGenericFoldCrossTypeIRX86_64 pins the #2686-tail fix: the cross-type
// closure-accumulator fold, driven through `if (fold(…))`, now lowers + runs on
// the self-hosted x86-64 IR path (the fn-typed argument inside the `if` condition
// is env-boxed by the lift pass). Routing is pinned to "ir".
func TestSelfHostGenericFoldCrossTypeIRX86_64(t *testing.T) {
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

	src := []byte(foldCrossTypeProg)
	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("fold-cross-type routed through %q path, want \"ir\"", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "fold_cross_type", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 5 {
		t.Errorf("fold-cross-type self-host x86-64 = %d, want 5", code)
	}
}

// TestSelfHostGenericFoldCrossTypeIRWasm is the wasm IR leg of the #2686-tail fix.
func TestSelfHostGenericFoldCrossTypeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fold-cross-type wasm IR e2e")
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

	src := []byte(foldCrossTypeProg)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader(src)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed for fold-cross-type: %v", err)
	}
	watFile := filepath.Join(dir, "fold_cross_type_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	runc := exec.Command("wasmtime", "run", watFile)
	_ = runc.Run()
	if runc.ProcessState == nil || !runc.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for fold-cross-type:\n%s", wat)
	}
	if code := runc.ProcessState.ExitCode(); code != 5 {
		t.Errorf("fold-cross-type wasm IR = %d, want 5", code)
	}
}

// TestNativeGenericIteratorCollector exercises bound-driven inference on the
// native interp / x86-64 / wasm backends, oracle-checked.
func TestNativeGenericIteratorCollector(t *testing.T) {
	for _, tc := range genericCollectorCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeGenericIteratorCollectorArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeGenericIteratorCollectorArm64(t *testing.T) {
	for _, tc := range genericCollectorCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericCollectorIRX86_64 routes each collector through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostGenericCollectorIRX86_64(t *testing.T) {
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

	for _, tc := range genericCollectorCases {
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

// TestSelfHostGenericCollectorIRWasm runs the same collectors through the wasm IR backend.
func TestSelfHostGenericCollectorIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-collector wasm IR e2e")
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

	for _, tc := range genericCollectorCases {
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
			watFile := filepath.Join(dir, "generic_collector_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("generic-collector wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
