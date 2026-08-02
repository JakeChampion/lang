package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic trait + a bound on a PARAMETRISED-trait instantiation
// (`function f[I: Iterator[i32]](…)`) on the self-host IR path. Before the
// bound-parser fix the self-host parser stopped at the `[` in `Iterator[i32]`
// and the function bailed the whole module to the legacy AST emitter; the
// parser now consumes the trait's bracketed type-arg list, so generic-trait
// bounds monomorphise and lower through the IR path on every backend — the
// keystone for a generic (not i32-locked) `Iterator[T]` (#2691 step 1 / #2686).
// The non-parametrised bound (`[T: Area]`) already worked; this pins the
// parametrised case, oracle-checked against the native backend.
var genericTraitBoundCases = []struct {
	name string
	main string
	want int
}{
	// generic trait, scalar method, bound on Trait[i32]
	{"generic-scalar", `pub trait Box[T] { function get(self: Self): T; }
struct IBox { v: i32 }
impl Box[i32] for IBox { function get(self: Self): i32 { return self.v; } }
function unwrap[B: Box[i32]](b: B): i32 { return b.get(); }
function main(): i32 { var b = IBox { v: 42 }; return unwrap(b); }`, 42},
	// generic trait, method returns Option[T], bound on Trait[i32]
	{"generic-option", `pub trait Peek[T] { function peek(self: Self): Option[T]; }
struct R { cur: i32, end: i32 }
impl Peek[i32] for R { function peek(self: Self): Option[i32] { if (self.cur >= self.end) { return None; } return Some(self.cur); } }
function head[P: Peek[i32]](p: P): i32 { match (p.peek()) { Some(v) => { return v + 8; }, None => { return 0; } } }
function main(): i32 { var r = R { cur: 2, end: 5 }; return head(r); }`, 10},
	// full generic Iterator[T]: Option[(T, Self)] + a bounded-generic driver
	{"generic-iterator-sum", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function sum_it[I: Iterator[i32]](it: I): i32 { var total = 0; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { total = total + t.0; cur = t.1; }, None => { go = false; }, } } return total; }
function main(): i32 { var r = RangeIter { cur: 0, end: 5 }; return sum_it(r); }`, 10},
	// TWO impls of one generic trait driven by ONE bounded-generic function →
	// two monomorphic clones, each dispatching to its own impl.
	{"generic-two-impls", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
struct Single { v: i32, done: boolean }
impl Iterator[i32] for Single { function next(self: Self): Option[(i32, Self)] { if (self.done) { return None; } return Some((self.v, Single { v: self.v, done: true })); } }
function sum_it[I: Iterator[i32]](it: I): i32 { var total = 0; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { total = total + t.0; cur = t.1; }, None => { go = false; }, } } return total; }
function main(): i32 { return sum_it(RangeIter { cur: 0, end: 5 }) + sum_it(Single { v: 7, done: false }); }`, 17},
}

// TestNativeGenericTraitBound runs the parametrised-trait-bound programs
// through the native interpreter + x86-64 + wasm backends (full pipeline incl.
// monomorph), oracle-checked.
func TestNativeGenericTraitBound(t *testing.T) {
	for _, tc := range genericTraitBoundCases {
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

// TestNativeGenericTraitBoundArm64 is the arm64 leg (CI-gated; runs under qemu).
func TestNativeGenericTraitBoundArm64(t *testing.T) {
	for _, tc := range genericTraitBoundCases {
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

// TestSelfHostGenericTraitBoundIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pins routing to "ir" (the parser fix keeps parametrised-
// trait bounds on the IR path), and oracle-checks the exit code.
func TestSelfHostGenericTraitBoundIRX86_64(t *testing.T) {
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

	for _, tc := range genericTraitBoundCases {
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

// TestSelfHostGenericTraitBoundIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostGenericTraitBoundIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-trait-bound wasm IR e2e")
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

	for _, tc := range genericTraitBoundCases {
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
			watFile := filepath.Join(dir, "generic_trait_bound_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("generic-trait-bound wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
