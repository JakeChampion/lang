package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var genericCollectorCases = []struct {
	name string
	main string
	want int
}{
	{"count-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function count[T, I: Iterator[T]](it: I): i32 { let n = 0; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { n = n + 1; cur = t.1; }, None => { go = false; }, } } return n; }
function main(): i32 { return count(RangeIter { cur: 0, end: 6 }); }`, 6},
	{"last-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { let acc = dflt; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return last(RangeIter { cur: 0, end: 5 }, -1); }`, 4},
	{"to-array-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function to_array[T, I: Iterator[T]](it: I): T[] { let out: T[] = []; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; }, None => { go = false; }, } } return out; }
function main(): i32 { let xs = to_array(RangeIter { cur: 0, end: 4 }); let s = 0; for x in xs { s = s + x; } return s + xs.len(); }`, 10},
	{"fold-sum", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function fold[T, A, I: Iterator[T]](it: I, init: A, f: (A, T) => A): A { let acc = init; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = f(acc, t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return fold(RangeIter { cur: 0, end: 5 }, 0, (a: i32, x: i32): i32 => { return a + x; }); }`, 10},
	{"nth-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function nth[T, I: Iterator[T]](it: I, n: i32): Option[T] { let cur = it; let k = n; let go = true; while (go) { match (cur.next()) { Some(t) => { if (k == 0) { return Some(t.0); } k = k - 1; cur = t.1; }, None => { go = false; }, } } return None; }
function main(): i32 { match (nth(RangeIter { cur: 0, end: 9 }, 4)) { Some(v) => { return v; }, None => { return 99; } } }`, 4},
	{"min-max-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function min[I: Iterator[i32]](it: I): Option[i32] { let cur = it; let best = 0; let seen = false; let go = true; while (go) { match (cur.next()) { Some(t) => { if (!seen || t.0 < best) { best = t.0; seen = true; } cur = t.1; }, None => { go = false; }, } } if (seen) { return Some(best); } return None; }
function max[I: Iterator[i32]](it: I): Option[i32] { let cur = it; let best = 0; let seen = false; let go = true; while (go) { match (cur.next()) { Some(t) => { if (!seen || t.0 > best) { best = t.0; seen = true; } cur = t.1; }, None => { go = false; }, } } if (seen) { return Some(best); } return None; }
function main(): i32 { let lo = 0; match (min(RangeIter { cur: 3, end: 7 })) { Some(v) => { lo = v; }, None => {} } let hi = 0; match (max(RangeIter { cur: 3, end: 7 })) { Some(v) => { hi = v; }, None => {} } return lo + hi; }`, 9},
	{"product-position", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function product[I: Iterator[i32]](it: I): i32 { let p = 1; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { p = p * t.0; cur = t.1; }, None => { go = false; }, } } return p; }
function position[I: Iterator[i32]](it: I, target: i32): Option[i32] { let cur = it; let i = 0; let go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { return Some(i); } i = i + 1; cur = t.1; }, None => { go = false; }, } } return None; }
function main(): i32 { let pr = product(RangeIter { cur: 1, end: 5 }); let po = 0; match (position(RangeIter { cur: 0, end: 9 }, 3)) { Some(v) => { po = v; }, None => {} } return pr - 7 * po; }`, 3},
	{"last-opt", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I): Option[T] { let cur = it; let acc: Option[T] = None; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = Some(t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { match (last(RangeIter { cur: 0, end: 5 })) { Some(v) => { return v; }, None => { return 99; } } }`, 4},
	{"contains-count-value", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function contains[I: Iterator[i32]](it: I, target: i32): boolean { let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { return true; } cur = t.1; }, None => { go = false; }, } } return false; }
function count_value[I: Iterator[i32]](it: I, target: i32): i32 { let n = 0; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { if (t.0 == target) { n = n + 1; } cur = t.1; }, None => { go = false; }, } } return n; }
function main(): i32 { let a = 0; if (contains(RangeIter { cur: 0, end: 5 }, 3)) { a = 5; } return a + count_value(RangeIter { cur: 0, end: 5 }, 2) + 1; }`, 7},
	{"last-bool", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct BoolSeq { n: i32 }
impl Iterator[boolean] for BoolSeq { function next(self: Self): Option[(boolean, Self)] { if (self.n <= 0) { return None; } return Some((true, BoolSeq { n: self.n - 1 })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { let acc = dflt; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { if (last(BoolSeq { n: 2 }, false)) { return 7; } return 0; }`, 7},
}

const foldCrossTypeProg = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function fold[T, A, I: Iterator[T]](it: I, init: A, f: (A, T) => A): A { let acc = init; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = f(acc, t.0); cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { if (fold(RangeIter { cur: 0, end: 4 }, true, (a: boolean, x: i32): boolean => { if (x < 10) { return a; } return false; })) { return 5; } return 0; }
`

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

func TestSelfHostGenericFoldCrossTypeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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

func TestSelfHostGenericFoldCrossTypeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fold-cross-type wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := []byte(foldCrossTypeProg)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

func TestSelfHostGenericCollectorIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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

func TestSelfHostGenericCollectorIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-collector wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range genericCollectorCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
