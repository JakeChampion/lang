package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var ordSortCases = []struct {
	name string
	main string
	want int
}{
	{"i32-ascending", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { let out: T[] = []; for x in xs { out = out.append(x); let i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { let tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { let s = sort([3, 1, 2]); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	{"i32-five", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { let out: T[] = []; for x in xs { out = out.append(x); let i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { let tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { let s = sort([5, 4, 3, 2, 1]); return s[0] * 100 + s[2] * 5 + s[4]; }`, 120},
	{"struct", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { v: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.v < other.v) { return 0 - 1; } if (self.v > other.v) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { let out: T[] = []; for x in xs { out = out.append(x); let i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { let tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { let xs: P[] = [P { v: 2 }, P { v: 1 }, P { v: 3 }]; let s = sort(xs); return s[0].v * 100 + s[1].v * 10 + s[2].v; }`, 123},
	{"stable", `pub trait Ord { function cmp(self: Self, other: Self): i32; }
struct P { key: i32, tag: i32 }
impl Ord for P { function cmp(self: Self, other: Self): i32 { if (self.key < other.key) { return 0 - 1; } if (self.key > other.key) { return 1; } return 0; } }
function sort[T: Ord](xs: T[]): T[] { let out: T[] = []; for x in xs { out = out.append(x); let i: i32 = out.len() - 1; while (i > 0 && out[i - 1].cmp(out[i]) > 0) { let tmp: T = out[i - 1]; out = out.with(i - 1, out[i]); out = out.with(i, tmp); i = i - 1; } } return out; }
function main(): i32 { let xs: P[] = [P { key: 1, tag: 2 }, P { key: 1, tag: 1 }, P { key: 0, tag: 0 }]; let s = sort(xs); return s[0].tag * 100 + s[1].tag * 10 + s[2].tag; }`, 21},
}

func TestNativeOrdSort(t *testing.T) {
	for _, tc := range ordSortCases {
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

func TestNativeOrdSortArm64(t *testing.T) {
	for _, tc := range ordSortCases {
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

func TestNativeOrdSortModule(t *testing.T) {
	src := `import "core/cmp" as cmp;
struct P { v: i32 }
impl cmp.Ord for P {
    function cmp(self: Self, other: Self): i32 {
        if (self.v < other.v) { return 0 - 1; }
        if (self.v > other.v) { return 1; }
        return 0;
    }
}
function main(): i32 {
    let r = 0;
    let s = cmp.sort([3, 1, 2]);
    if (s[0] == 1 && s[1] == 2 && s[2] == 3) { r = r + 1; }
    let ss = cmp.sort(["banana", "apple", "cherry"]);
    if (ss[0] == "apple" && ss[1] == "banana" && ss[2] == "cherry") { r = r + 2; }
    let ps = cmp.sort([P { v: 2 }, P { v: 1 }, P { v: 3 }]);
    if (ps[0].v == 1 && ps[1].v == 2 && ps[2].v == 3) { r = r + 4; }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 7 {
		t.Errorf("ord-sort module interp = %d, want 7", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 7 {
		t.Errorf("ord-sort module x86-64 = %d, want 7", code)
	}
	if code := runWasm(t, src); code != 7 {
		t.Errorf("ord-sort module wasm = %d, want 7", code)
	}
}

func TestSelfHostOrdSortIRX86_64(t *testing.T) {
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

	for _, tc := range ordSortCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.main+"\n"))
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

func TestSelfHostOrdSortIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ord-sort wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range ordSortCases {
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
			watFile := filepath.Join(dir, "ord_sort_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ord-sort wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
