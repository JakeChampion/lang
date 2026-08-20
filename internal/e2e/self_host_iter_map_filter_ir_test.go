package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Eager iterator transforms `map` / `filter` in core/iter — closure-taking
// adapters that consume an Iterator and collect into a fresh array (#2691, the
// iterator-protocol surface that feeds `collect` #2709). Like the predicate
// adapters, the closure is applied inside a `match` arm, and the whole driver is
// called in a `var` / `for-in` position; both lower on the self-host IR path now
// that the #2686 condition-lift gap is fixed. The inline prelude mirrors
// core/iter's `map` / `filter` so both backends exercise byte-for-byte the same
// shape.
const iterMapFilterPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function map[T, U, I: Iterator[T]](it: I, f: (T) => U): U[] {
    var out: U[] = []; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { out = out.append(f(t.0)); cur = t.1; }, None => { go = false; }, } }
    return out;
}
pub function filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[] {
    var out: T[] = []; var cur = it; var go = true;
    while (go) { match (cur.next()) { Some(t) => { if (keep(t.0)) { out = out.append(t.0); } cur = t.1; }, None => { go = false; }, } }
    return out;
}
`

var iterMapFilterCases = []struct {
	name string
	main string
	want int
}{
	// map x -> x*x over [0,4): [0,1,4,9]; sum 14 + len 4 = 18.
	{"map-square", `function main(): i32 { var sq = map(range(0, 4), function (x: i32): i32 { return x * x; }); var s = 0; for v in sq { s = s + v; } return s + sq.len(); }`, 18},
	// filter even over [0,8): [0,2,4,6]; sum 12 + len 4 = 16.
	{"filter-even", `function main(): i32 { var ev = filter(range(0, 8), function (x: i32): boolean { return x % 2 == 0; }); var t = 0; for v in ev { t = t + v; } return t + ev.len(); }`, 16},
	// map to a DIFFERENT element type (U ≠ T): i32 → boolean (is-even) over [0,5)
	// = [T,F,T,F,T]; count the trues → 3.
	{"map-to-bool", `function main(): i32 { var bs = map(range(0, 5), function (x: i32): boolean { return x % 2 == 0; }); var c = 0; for b in bs { if (b) { c = c + 1; } } return c; }`, 3},
	// map over an empty range yields an empty array → len 0; +7 = 7.
	{"map-empty", `function main(): i32 { var e = map(range(3, 3), function (x: i32): i32 { return x + 1; }); return e.len() + 7; }`, 7},
}

func iterMapFilterProg(mainBody string) string { return iterMapFilterPrelude + mainBody + "\n" }

// TestNativeIterMapFilter runs the inline map/filter programs on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeIterMapFilter(t *testing.T) {
	for _, tc := range iterMapFilterCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterMapFilterProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterMapFilterProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterMapFilterArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterMapFilterArm64(t *testing.T) {
	for _, tc := range iterMapFilterCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterMapFilterProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterMapFilterModule exercises the shipped `import "core/iter"`
// module's `map` / `filter` over real `range` iterators on the native backends.
func TestNativeIterMapFilterModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var sq = iter.map(iter.range(0, 4), function (x: i32): i32 { return x * x; });   // [0,1,4,9]
    var s = 0; for v in sq { s = s + v; }                                            // 14
    var ev = iter.filter(iter.range(0, 8), function (x: i32): boolean { return x % 2 == 0; });  // [0,2,4,6]
    var t = 0; for v in ev { t = t + v; }                                            // 12
    return s + t + sq.len() + ev.len();                                              // 14+12+4+4 = 34
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 34 {
		t.Errorf("map/filter module interp = %d, want 34", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 34 {
		t.Errorf("map/filter module x86-64 = %d, want 34", code)
	}
	if code := runWasm(t, src); code != 34 {
		t.Errorf("map/filter module wasm = %d, want 34", code)
	}
}

// TestSelfHostIterMapFilterIRX86_64 routes each inline case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterMapFilterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterMapFilterCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterMapFilterProg(tc.main))
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

// TestSelfHostIterMapFilterIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIterMapFilterIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter map/filter wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range iterMapFilterCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterMapFilterProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_map_filter_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter map/filter wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
