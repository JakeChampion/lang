package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tuple-producing iterator adapters `enumerate` / `zip` in core/iter (#2691).
// `enumerate` numbers each value into `(i32, T)[]`; `zip` walks two iterators in
// lockstep into `(T, U)[]`, stopping at the shorter. Despite returning a tuple
// array from a bounded generic — and the driver reading `p.0` / `p.1` field
// positions off that generic return — both lower on the self-host IR path (the
// generic tuple-array field access works; an earlier belief that it didn't was a
// process-exit-code (u8) truncation artifact in the test oracle, not a codegen
// limit). All oracles here stay < 256 so the i32 result survives the exit code.
const iterEnumZipPrelude = `pub trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
pub struct Range { lo: i32, hi: i32 }
pub function range(lo: i32, hi: i32): Range { return Range { lo: lo, hi: hi }; }
impl Iterator[i32] for Range {
    function next(self: Self): Option[(i32, Self)] {
        if (self.lo >= self.hi) { return None; }
        return Some((self.lo, Range { lo: self.lo + 1, hi: self.hi }));
    }
}
pub function enumerate[T, I: Iterator[T]](it: I): (i32, T)[] {
    var out: (i32, T)[] = []; var cur = it; var i = 0; var go = true;
    while (go) { match (cur.next()) { Some(t) => { out = out.append((i, t.0)); cur = t.1; i = i + 1; }, None => { go = false; }, } }
    return out;
}
pub function zip[T, U, I: Iterator[T], J: Iterator[U]](a: I, b: J): (T, U)[] {
    var out: (T, U)[] = []; var ca = a; var cb = b; var go = true;
    while (go) { match (ca.next()) { Some(ta) => { match (cb.next()) { Some(tb) => { out = out.append((ta.0, tb.0)); ca = ta.1; cb = tb.1; }, None => { go = false; }, } }, None => { go = false; }, } }
    return out;
}
`

var iterEnumZipCases = []struct {
	name string
	main string
	want int
}{
	// enumerate [10,11,12,13] -> [(0,10),(1,11),(2,12),(3,13)]; sum(i+v)=52, +len 4 = 56.
	{"enumerate", `function main(): i32 { var e = enumerate(range(10, 14)); var s = 0; for p in e { s = s + p.0 + p.1; } return s + e.len(); }`, 56},
	// enumerate over an empty range -> []; len 0 + 9 = 9.
	{"enumerate-empty", `function main(): i32 { var e = enumerate(range(5, 5)); return e.len() + 9; }`, 9},
	// zip equal lengths: [(0,10),(1,11),(2,12)]; sum(p.0+p.1)=36, +len 3 = 39.
	{"zip-equal", `function main(): i32 { var z = zip(range(0, 3), range(10, 13)); var s = 0; for p in z { s = s + p.0 + p.1; } return s + z.len(); }`, 39},
	// zip stops at the shorter (first): [(0,10),(1,11)]; sum=22, +len 2 = 24.
	{"zip-short-first", `function main(): i32 { var z = zip(range(0, 2), range(10, 99)); var s = 0; for p in z { s = s + p.0 + p.1; } return s + z.len(); }`, 24},
	// zip stops at the shorter (second): [(0,5),(1,6)]; sum=12, +len 2 = 14.
	{"zip-short-second", `function main(): i32 { var z = zip(range(0, 99), range(5, 7)); var s = 0; for p in z { s = s + p.0 + p.1; } return s + z.len(); }`, 14},
}

func iterEnumZipProg(mainBody string) string { return iterEnumZipPrelude + mainBody + "\n" }

// TestNativeIterEnumZip runs the inline enumerate/zip programs on the native
// interp / x86-64 / wasm backends, oracle-checked.
func TestNativeIterEnumZip(t *testing.T) {
	for _, tc := range iterEnumZipCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterEnumZipProg(tc.main))
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, iterEnumZipProg(tc.main)); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterEnumZipArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeIterEnumZipArm64(t *testing.T) {
	for _, tc := range iterEnumZipCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeIterProg(t, iterEnumZipProg(tc.main))
			if _, code := runFixtureArm64(t, p, ""); code != tc.want {
				t.Errorf("%s arm64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeIterEnumZipModule exercises the shipped `import "core/iter"`
// module's enumerate / zip over real `range` iterators on the native backends.
func TestNativeIterEnumZipModule(t *testing.T) {
	src := `import "core/iter" as iter;
function main(): i32 {
    var e = iter.enumerate(iter.range(10, 14));                   // [(0,10),(1,11),(2,12),(3,13)]
    var s = 0; for p in e { s = s + p.0 + p.1; }                  // 6 + 46 = 52
    var z = iter.zip(iter.range(0, 4), iter.range(100, 102));     // [(0,100),(1,101)] (shorter wins)
    var t = 0; for p in z { t = t + p.0; }                        // 0+1 = 1
    return s + e.len() + z.len() + t;                             // 52+4+2+1 = 59
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 59 {
		t.Errorf("enumerate/zip module interp = %d, want 59", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 59 {
		t.Errorf("enumerate/zip module x86-64 = %d, want 59", code)
	}
	if code := runWasm(t, src); code != 59 {
		t.Errorf("enumerate/zip module wasm = %d, want 59", code)
	}
}

// TestSelfHostIterEnumZipIRX86_64 routes each inline case through the
// self-hosted x86-64 IR driver, pins routing to "ir", and oracle-checks it.
func TestSelfHostIterEnumZipIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range iterEnumZipCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterEnumZipProg(tc.main))
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

// TestSelfHostIterEnumZipIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostIterEnumZipIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host iter enumerate/zip wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range iterEnumZipCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(iterEnumZipProg(tc.main))
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
			watFile := filepath.Join(dir, "iter_enum_zip_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("iter enumerate/zip wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
