package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Bounded iterator collectors `take` / `skip` in core/iter — the eager
// prefix/suffix forms next to map / filter (#2691, the iterator-protocol
// surface). `take(it, k)` collects the first k yielded values; `skip(it, k)`
// drops them and collects the rest. Both return a fresh `T[]` (the same safe
// shape as filter — no tuple/struct field access on a generic return), so they
// lower on the self-host IR path. The inline prelude mirrors core/iter's bodies
// so both backends exercise byte-for-byte the same shape.
const iterTakeSkipPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function take[T, I: Iterator[T]](it: I, n: i32): T[] {
    var out: T[] = []; var cur = it; var k = n; var go = true;
    while (go) { if (k <= 0) { go = false; } else { match (cur.next()) { Some(t) => { out = out.append(t.0); cur = t.1; k = k - 1; }, None => { go = false; }, } } }
    return out;
}
pub function skip[T, I: Iterator[T]](it: I, n: i32): T[] {
    var out: T[] = []; var cur = it; var k = n; var go = true;
    while (go) { match (cur.next()) { Some(t) => { if (k > 0) { k = k - 1; } else { out = out.append(t.0); } cur = t.1; }, None => { go = false; }, } }
    return out;
}
`

var iterTakeSkipCases = []struct {
	name string
	main string
	want int
}{
	// take the first 4 of [0,100): [0,1,2,3]; sum 6 + len 4 = 10.
	{"take", `function main(): i32 { var a = take(range(0, 100), 4); var s = 0; for v in a { s = s + v; } return s + a.len(); }`, 10},
	// take more than available: take 9 of [0,3) yields [0,1,2]; sum 3 + len 3 = 6.
	{"take-saturating", `function main(): i32 { var a = take(range(0, 3), 9); var s = 0; for v in a { s = s + v; } return s + a.len(); }`, 6},
	// skip the first 4 of [0,6): [4,5]; sum 9 + len 2 = 11.
	{"skip", `function main(): i32 { var b = skip(range(0, 6), 4); var s = 0; for v in b { s = s + v; } return s + b.len(); }`, 11},
	// skip past the end: skip 10 of [0,3) yields []; len 0 + 5 = 5.
	{"skip-past-end", `function main(): i32 { var b = skip(range(0, 3), 10); return b.len() + 5; }`, 5},
	// take(0) is empty; skip(0) keeps all of [0,4) = sum 6; 0 + 6 = 6.
	{"take0-skip0", `function main(): i32 { var a = take(range(0, 4), 0); var b = skip(range(0, 4), 0); var s = 0; for v in b { s = s + v; } return a.len() + s; }`, 6},
}

func iterTakeSkipProg(mainBody string) string { return iterTakeSkipPrelude + mainBody + "\n" }

// TestNativeIterTakeSkip runs the inline take/skip programs on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeIterTakeSkip(t *testing.T) {
	for _, tc := range iterTakeSkipCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterTakeSkipProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterTakeSkipProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterTakeSkipArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterTakeSkipArm64(t *testing.T) {
	for _, tc := range iterTakeSkipCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterTakeSkipProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterTakeSkipModule exercises the shipped `import "core/iter"`
// module's `take` / `skip` over real `range` iterators on the native backends.
func TestNativeIterTakeSkipModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var a = iter.take(iter.range(0, 100), 4);  // [0,1,2,3]
    var sa = 0; for v in a { sa = sa + v; }     // 6
    var b = iter.skip(iter.range(0, 6), 4);     // [4,5]
    var sb = 0; for v in b { sb = sb + v; }     // 9
    return sa + a.len() + sb + b.len();         // 6+4+9+2 = 21
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 21 {
		t.Errorf("take/skip module interp = %d, want 21", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 21 {
		t.Errorf("take/skip module x86-64 = %d, want 21", code)
	}
	if code := runWasm(t, src); code != 21 {
		t.Errorf("take/skip module wasm = %d, want 21", code)
	}
}

// TestSelfHostIterTakeSkipIRX86_64 routes each inline case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterTakeSkipIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterTakeSkipCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterTakeSkipProg(tc.main))
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

// TestSelfHostIterTakeSkipIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIterTakeSkipIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter take/skip wasm IR e2e")
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

	for _, tc := range iterTakeSkipCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterTakeSkipProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_take_skip_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter take/skip wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
