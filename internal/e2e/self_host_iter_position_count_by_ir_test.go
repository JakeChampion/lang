package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Closure-predicate index/count adapters `position_by` / `count_by` in core/iter
// — the element-type-generic counterparts to the i32-only `position` /
// `count_value` (#2691). `position_by` returns the index of the first match (or
// -1); `count_by` counts the matches. Both return a scalar i32 (no Option[T]
// accumulator / tuple return), and the closure is applied inside an `if` the
// #2686 condition-lift fix now covers — so they lower on the self-host IR path.
const iterPositionCountByPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function position_by[T, I: Iterator[T]](it: I, pred: (T) => boolean): i32 {
    var cur = it; var i = 0; var go = true;
    while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return i; } i = i + 1; cur = t.1; }, None => { go = false; }, } }
    return 0 - 1;
}
pub function count_by[T, I: Iterator[T]](it: I, pred: (T) => boolean): i32 {
    var cur = it; var n = 0; var go = true;
    while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { n = n + 1; } cur = t.1; }, None => { go = false; }, } }
    return n;
}
`

var iterPositionCountByCases = []struct {
	name string
	main string
	want int
}{
	// position_by: first x with x*x > 8 over [0,10) is 3.
	{"position-hit", `function main(): i32 { return position_by(range(0, 10), function (x: i32): boolean { return x * x > 8; }); }`, 3},
	// position_by: no match → -1; +6 = 5.
	{"position-miss", `function main(): i32 { return position_by(range(0, 4), function (x: i32): boolean { return x > 99; }) + 6; }`, 5},
	// count_by: multiples of 3 in [0,10): 0,3,6,9 → 4.
	{"count", `function main(): i32 { return count_by(range(0, 10), function (x: i32): boolean { return x % 3 == 0; }); }`, 4},
	// count_by: none match → 0; +7 = 7.
	{"count-none", `function main(): i32 { return count_by(range(0, 5), function (x: i32): boolean { return x > 99; }) + 7; }`, 7},
}

func iterPositionCountByProg(mainBody string) string {
	return iterPositionCountByPrelude + mainBody + "\n"
}

// TestNativeIterPositionCountBy runs the inline programs on the native interp /
// x86-64 / wasm backends, oracle-checked.
func TestNativeIterPositionCountBy(t *testing.T) {
	for _, tc := range iterPositionCountByCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterPositionCountByProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterPositionCountByProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterPositionCountByArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterPositionCountByArm64(t *testing.T) {
	for _, tc := range iterPositionCountByCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterPositionCountByProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterPositionCountByModule exercises the shipped `import "core/iter"`
// module's position_by / count_by over real `range` iterators.
func TestNativeIterPositionCountByModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var p = iter.position_by(iter.range(0, 10), function (x: i32): boolean { return x * x > 8; });  // 3
    var c = iter.count_by(iter.range(0, 10), function (x: i32): boolean { return x % 3 == 0; });     // 4
    var m = iter.position_by(iter.range(0, 4), function (x: i32): boolean { return x > 99; });        // -1
    return p * 10 + c + (m + 1);                                                                      // 30+4+0 = 34
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 34 {
		t.Errorf("position_by/count_by module interp = %d, want 34", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 34 {
		t.Errorf("position_by/count_by module x86-64 = %d, want 34", code)
	}
	if code := runWasm(t, src); code != 34 {
		t.Errorf("position_by/count_by module wasm = %d, want 34", code)
	}
}

// TestSelfHostIterPositionCountByIRX86_64 routes each inline case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterPositionCountByIRX86_64(t *testing.T) {
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

	for _, tc := range iterPositionCountByCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterPositionCountByProg(tc.main))
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

// TestSelfHostIterPositionCountByIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIterPositionCountByIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter position_by/count_by wasm IR e2e")
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

	for _, tc := range iterPositionCountByCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterPositionCountByProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_position_count_by_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter position_by/count_by wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
