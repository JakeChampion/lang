package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var predicateAdapterPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function any[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return true; } cur = t.1; }, None => { go = false; }, } } return false; }
function all[T, I: Iterator[T]](it: I, pred: (T) => boolean): boolean { let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { if (!pred(t.0)) { return false; } cur = t.1; }, None => { go = false; }, } } return true; }
function find[T, I: Iterator[T]](it: I, pred: (T) => boolean): Option[T] { let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return Some(t.0); } cur = t.1; }, None => { go = false; }, } } return None; }
`

var predicateAdapterCases = []struct {
	name string
	main string
	want int
}{
	{"any-hit", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, (x: i32): boolean => { return x == 3; })) { return 5; } return 0; }`, 5},
	{"any-miss", `function main(): i32 { if (any(RangeIter { cur: 0, end: 5 }, (x: i32): boolean => { return x > 9; })) { return 0; } return 9; }`, 9},
	{"all-true", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, (x: i32): boolean => { return x < 10; })) { return 6; } return 0; }`, 6},
	{"all-false", `function main(): i32 { if (all(RangeIter { cur: 0, end: 5 }, (x: i32): boolean => { return x % 2 == 0; })) { return 0; } return 8; }`, 8},
	{"find-some", `function main(): i32 { match (find(RangeIter { cur: 0, end: 9 }, (x: i32): boolean => { return x >= 2 && x % 2 == 0; })) { Some(v) => { return v; }, None => { return 99; } } }`, 2},
	{"find-none", `function main(): i32 { match (find(RangeIter { cur: 0, end: 3 }, (x: i32): boolean => { return x > 100; })) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

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

func TestSelfHostGenericPredicateAdaptersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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

func TestSelfHostGenericPredicateAdaptersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host predicate-adapter wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range predicateAdapterCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(predicateAdapterPrelude + tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
