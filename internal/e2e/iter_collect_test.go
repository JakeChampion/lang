package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `collect` — the canonical terminal of an iterator chain (#2709): drains an
// `Iterator[T]` into a fresh `T[]`. Same bounded-collector shape as the shipped
// `to_array` (`T` recovered by bound-driven inference on the native side, erased
// + monomorphised on `I` for the self-host IR path), so it lowers on every
// native backend AND the self-host IR path. The inline cases pin the routing /
// genericity (i32 + a boolean element type); a native module test drives the
// real `core/iter` body. (The keyed `Map` sink `to_map` is deferred — see the
// note in core/iter.fern: a generic `Map[K, V]` in a generic body hits the same
// abstract-key dispatch gap as the generic `Set[T]` in #2671.)
var iterCollectCases = []struct {
	name string
	main string
	want int
}{
	// collect an i32 range into i32[]; sum(0..4) + len = 6 + 4 = 10.
	{"collect-i32", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function collect[T, I: Iterator[T]](it: I): T[] { var out: T[] = []; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; }, None => { go = false; }, } } return out; }
function main(): i32 { var xs = collect(RangeIter { cur: 0, end: 4 }); var s = 0; for x in xs { s = s + x; } return s + xs.len(); }`, 10},
	// the SAME generic collect at T=boolean: collect 3 trues, count them → 3.
	{"collect-bool", `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct BoolSeq { n: i32 }
impl Iterator[boolean] for BoolSeq { function next(self: Self): Option[(boolean, Self)] { if (self.n <= 0) { return None; } return Some((true, BoolSeq { n: self.n - 1 })); } }
function collect[T, I: Iterator[T]](it: I): T[] { var out: T[] = []; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; }, None => { go = false; }, } } return out; }
function main(): i32 { var xs = collect(BoolSeq { n: 3 }); var c = 0; for b in xs { if (b) { c = c + 1; } } return c; }`, 3},
}

// TestNativeIterCollect runs the inline collect cases on interp / x86-64 / wasm.
func TestNativeIterCollect(t *testing.T) {
	for _, tc := range iterCollectCases {
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

// TestNativeIterCollectArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterCollectArm64(t *testing.T) {
	for _, tc := range iterCollectCases {
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

// TestNativeIterCollectModule drives the shipped `core/iter` `collect` over the
// real `of` (ArrayIter) and `range` (Range) iterators, oracle-checked.
func TestNativeIterCollectModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var r = 0;
    var ys = iter.collect(iter.of([3, 1, 2]));
    if (ys.len() == 3 && ys[0] == 3 && ys[1] == 1 && ys[2] == 2) { r = r + 1; }
    var zs = iter.collect(iter.range(0, 4));
    if (zs.len() == 4 && zs[0] == 0 && zs[3] == 3) { r = r + 2; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 3 {
		t.Errorf("iter.collect module interp = %d, want 3", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 3 {
		t.Errorf("iter.collect module x86-64 = %d, want 3", code)
	}
	if code := runWasm(t, src); code != 3 {
		t.Errorf("iter.collect module wasm = %d, want 3", code)
	}
}

// TestSelfHostIterCollectIRX86_64 routes each inline collect case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterCollectIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterCollectCases {
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

// TestSelfHostIterCollectIRWasm runs the same inline collect cases through the wasm IR backend.
func TestSelfHostIterCollectIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter-collect wasm IR e2e")
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

	for _, tc := range iterCollectCases {
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
			watFile := filepath.Join(dir, "iter_collect_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter-collect wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
