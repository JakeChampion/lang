package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const iterPositionCountWherePrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function position[T, I: Iterator[T]](it: I, pred: (T) => boolean): Option[i32] {
    let cur = it; let i = 0; let go = true;
    while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { return Some(i); } i = i + 1; cur = t.1; }, None => { go = false; }, } }
    return None;
}
pub function count_where[T, I: Iterator[T]](it: I, pred: (T) => boolean): i32 {
    let cur = it; let n = 0; let go = true;
    while (go) { match (cur.next()) { Some(t) => { if (pred(t.0)) { n = n + 1; } cur = t.1; }, None => { go = false; }, } }
    return n;
}
`

var iterPositionCountWhereCases = []struct {
	name string
	main string
	want int
}{
	{"position-hit", `function main(): i32 { match (position(range(0, 10), (x: i32): boolean => { return x * x > 8; })) { Some(i) => { return i; }, None => { return 9; } } }`, 3},
	{"position-miss", `function main(): i32 { match (position(range(0, 4), (x: i32): boolean => { return x > 99; })) { Some(i) => { return i; }, None => { return 5; } } }`, 5},
	{"count", `function main(): i32 { return count_where(range(0, 10), (x: i32): boolean => { return x % 3 == 0; }); }`, 4},
	{"count-none", `function main(): i32 { return count_where(range(0, 5), (x: i32): boolean => { return x > 99; }) + 7; }`, 7},
}

func iterPositionCountWhereProg(mainBody string) string {
	return iterPositionCountWherePrelude + mainBody + "\n"
}

func TestNativeIterPositionCountWhere(t *testing.T) {
	for _, tc := range iterPositionCountWhereCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterPositionCountWhereProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterPositionCountWhereProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestNativeIterPositionCountWhereArm64(t *testing.T) {
	for _, tc := range iterPositionCountWhereCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterPositionCountWhereProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestNativeIterPositionCountWhereModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    let p = 0;
    match (iter.position(iter.range(0, 10), (x: i32): boolean => { return x * x > 8; })) { Some(i) => { p = i; }, None => {} }  // 3
    let c = iter.count_where(iter.range(0, 10), (x: i32): boolean => { return x % 3 == 0; });                                  // 4
    match (iter.position(iter.range(0, 4), (x: i32): boolean => { return x > 99; })) { Some(_) => { return 1; }, None => {} }
    return p * 10 + c;                                                                                                          // 30+4 = 34
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 34 {
		t.Errorf("position/count_where module interp = %d, want 34", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 34 {
		t.Errorf("position/count_where module x86-64 = %d, want 34", code)
	}
	if code := runWasm(t, src); code != 34 {
		t.Errorf("position/count_where module wasm = %d, want 34", code)
	}
}

func TestSelfHostIterPositionCountWhereIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterPositionCountWhereCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterPositionCountWhereProg(tc.main))
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

func TestSelfHostIterPositionCountWhereIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter position/count_where wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range iterPositionCountWhereCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterPositionCountWhereProg(tc.main))
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
				t.Errorf("iter position/count_where wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
