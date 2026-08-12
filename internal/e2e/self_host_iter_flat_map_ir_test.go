package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// flat_map in core/iter (#2691 / the "flat_map too" note in #2689) — applies a
// `(T) => U[]` closure to each value and concatenates the per-element arrays into
// one fresh `U[]`. The closure RETURNS an array (a move) and its result is
// iterated with an inner `for y in ys`; both lower on the self-host IR path. All
// oracles stay < 256 so the i32 result survives the process exit code.
const iterFlatMapPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function flat_map[T, U, I: Iterator[T]](it: I, f: (T) => U[]): U[] {
    var out: U[] = []; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { var ys = f(t.0); for y in ys { out = out.append(y); } cur = t.1; }, None => { go = false; }, } }
    return out;
}
`

var iterFlatMapCases = []struct {
	name string
	main string
	want int
}{
	// fan each x to [x, x*10] over [1,4): [1,10,2,20,3,30]; sum 66 + len 6 = 72.
	{"fan-out", `function main(): i32 { var r = flat_map(range(1, 4), function (x: i32): i32[] { return [x, x * 10]; }); var s = 0; for v in r { s = s + v; } return s + r.len(); }`, 72},
	// drop evens (return []), keep odds over [0,6): [1,3,5]; sum 9 + len 3 = 12.
	{"drop-and-keep", `function main(): i32 { var r = flat_map(range(0, 6), function (x: i32): i32[] { if (x % 2 == 0) { return []; } return [x]; }); var s = 0; for v in r { s = s + v; } return s + r.len(); }`, 12},
	// every element maps to [] → empty result; len 0 + 5 = 5.
	{"all-empty", `function main(): i32 { var r = flat_map(range(0, 4), function (x: i32): i32[] { return []; }); return r.len() + 5; }`, 5},
	// flat_map over an empty source → empty; len 0 + 8 = 8.
	{"empty-source", `function main(): i32 { var r = flat_map(range(3, 3), function (x: i32): i32[] { return [x, x]; }); return r.len() + 8; }`, 8},
}

func iterFlatMapProg(mainBody string) string { return iterFlatMapPrelude + mainBody + "\n" }

// TestNativeIterFlatMap runs the inline flat_map programs on the native interp /
// x86-64 / wasm backends, oracle-checked.
func TestNativeIterFlatMap(t *testing.T) {
	for _, tc := range iterFlatMapCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterFlatMapProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterFlatMapProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterFlatMapArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterFlatMapArm64(t *testing.T) {
	for _, tc := range iterFlatMapCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterFlatMapProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterFlatMapModule exercises the shipped `import "core/iter"` module's
// flat_map over real `range` iterators on the native backends.
func TestNativeIterFlatMapModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var r = iter.flat_map(iter.range(1, 4), function (x: i32): i32[] { return [x, x * 10]; });  // [1,10,2,20,3,30]
    var s = 0; for v in r { s = s + v; }                                                          // 66
    var o = iter.flat_map(iter.range(0, 6), function (x: i32): i32[] { if (x % 2 == 0) { return []; } return [x]; });  // [1,3,5]
    var t = 0; for v in o { t = t + v; }                                                          // 9
    return s + r.len() + t + o.len();                                                             // 66+6+9+3 = 84
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 84 {
		t.Errorf("flat_map module interp = %d, want 84", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 84 {
		t.Errorf("flat_map module x86-64 = %d, want 84", code)
	}
	if code := runWasm(t, src); code != 84 {
		t.Errorf("flat_map module wasm = %d, want 84", code)
	}
}

// TestSelfHostIterFlatMapIRX86_64 routes each inline case through the self-hosted
// x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterFlatMapIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterFlatMapCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterFlatMapProg(tc.main))
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

// TestSelfHostIterFlatMapIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIterFlatMapIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter flat_map wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range iterFlatMapCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterFlatMapProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_flat_map_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter flat_map wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
