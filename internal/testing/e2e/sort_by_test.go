package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var sortByCases = []struct {
	name string
	main string
	want int
}{
	{"sort-by-desc", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { let out: T[] = arr; let n = out.len(); let i = 1; while (i < n) { let j = i; while (j > 0 && cmp(out[j], out[j - 1]) < 0) { let tmp: T = out[j]; out = out.with(j, out[j - 1]); out = out.with(j - 1, tmp); j = j - 1; } i = i + 1; } return out; }
function main(): i32 { let xs: i32[] = [5, 1, 9, 2]; let s = sort_by(xs, (a: i32, b: i32): i32 => { if (a > b) { return 0 - 1; } if (a < b) { return 1; } return 0; }); return s[0] * 10 + s[3]; }`, 91},
	{"sort-by-asc", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { let out: T[] = arr; let n = out.len(); let i = 1; while (i < n) { let j = i; while (j > 0 && cmp(out[j], out[j - 1]) < 0) { let tmp: T = out[j]; out = out.with(j, out[j - 1]); out = out.with(j - 1, tmp); j = j - 1; } i = i + 1; } return out; }
function main(): i32 { let xs: i32[] = [3, 1, 2]; let s = sort_by(xs, (a: i32, b: i32): i32 => { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	{"is-sorted-by", `function is_sorted_by[T](arr: T[], cmp: (T, T) => i32): boolean { let i = 1; let n = arr.len(); while (i < n) { if (cmp(arr[i], arr[i - 1]) < 0) { return false; } i = i + 1; } return true; }
function asc(a: i32, b: i32): i32 { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }
function main(): i32 { let a: i32[] = [1, 2, 3]; let b: i32[] = [3, 1]; let r = 0; if (is_sorted_by(a, asc)) { r = r + 5; } if (!is_sorted_by(b, asc)) { r = r + 2; } return r; }`, 7},
	{"sort-by-merge", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { let n = arr.len(); if (n < 2) { return arr; } let src: T[] = arr; let width = 1; while (width < n) { let dst: T[] = src; let lo = 0; while (lo < n) { let mid = lo + width; if (mid > n) { mid = n; } let hi = lo + width + width; if (hi > n) { hi = n; } let i = lo; let j = mid; let k = lo; while (i < mid && j < hi) { if (cmp(src[j], src[i]) < 0) { dst = dst.with(k, src[j]); j = j + 1; } else { dst = dst.with(k, src[i]); i = i + 1; } k = k + 1; } while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; } while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; } lo = lo + width + width; } src = dst; width = width + width; } return src; }
function main(): i32 { let xs: i32[] = [8, 3, 3, 9, 1, 7, 2, 5, 0, 6]; let s = sort_by(xs, (a: i32, b: i32): i32 => { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }); return s[0] * 100 + s[5] * 10 + s[9]; }`, 59},
}

func TestNativeSortBy(t *testing.T) {
	for _, tc := range sortByCases {
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

func TestNativeSortByArm64(t *testing.T) {
	for _, tc := range sortByCases {
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

func TestNativeSortByModule(t *testing.T) {
	src := `import "core/cmp";
function desc(a: i32, b: i32): i32 { if (a > b) { return 0 - 1; } if (a < b) { return 1; } return 0; }
function main(): i32 {
    let s = cmp.sort_by([5, 1, 9, 2], desc); // [9,5,2,1]
    let ok = 0;
    if (cmp.is_sorted_by(s, desc)) { ok = 1; } // 1
    return s[0] * 10 + s[3] + ok;               // 91 + 1 = 92
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 92 {
		t.Errorf("sort_by module interp = %d, want 92", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 92 {
		t.Errorf("sort_by module x86-64 = %d, want 92", code)
	}
	if code := runWasm(t, src); code != 92 {
		t.Errorf("sort_by module wasm = %d, want 92", code)
	}
}

func TestSelfHostSortByIRX86_64(t *testing.T) {
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

	for _, tc := range sortByCases {
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
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

const sortByKeyProg = `import "core/cmp";
struct P { k: i32, tag: i32 }
function main(): i32 {
    let xs: P[] = [P { k: 3, tag: 0 }, P { k: 1, tag: 0 }, P { k: 2, tag: 0 }];
    let s = cmp.sort_by_key(xs, (p: P): i32 => { return p.k; });
    return s[0].k * 100 + s[1].k * 10 + s[2].k;
}
`

func TestNativeSortByKey(t *testing.T) {
	p := writeIterProg(t, sortByKeyProg)
	if _, code := runFixtureInterp(t, p, ""); code != 123 {
		t.Errorf("sort_by_key interp = %d, want 123", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 123 {
		t.Errorf("sort_by_key x86-64 = %d, want 123", code)
	}
	if code := runWasm(t, sortByKeyProg); code != 123 {
		t.Errorf("sort_by_key wasm = %d, want 123", code)
	}
}

func TestNativeSortByKeyArm64(t *testing.T) {
	p := writeIterProg(t, sortByKeyProg)
	if _, code := runFixtureArm64(t, p, ""); code != 123 {
		t.Errorf("sort_by_key arm64 = %d, want 123", code)
	}
}

func TestSelfHostSortByI32Key(t *testing.T) {
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
	prog := `struct P { k: i32, tag: i32 }
function sort_by_i32_key[T](arr: T[], key: (T) => i32): T[] {
    let out: T[] = arr;
    let keys: i32[] = [];
    for x in out { keys = keys.append(key(x)); }
    let n: i32 = out.len();
    let i: i32 = 1;
    while (i < n) {
        let j: i32 = i;
        while (j > 0 && keys[j] < keys[j - 1]) {
            let tv: T = out[j];
            out = out.with(j, out[j - 1]);
            out = out.with(j - 1, tv);
            let tk: i32 = keys[j];
            keys = keys.with(j, keys[j - 1]);
            keys = keys.with(j - 1, tk);
            j = j - 1;
        }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let xs: P[] = [P { k: 3, tag: 0 }, P { k: 1, tag: 0 }, P { k: 2, tag: 0 }];
    let s = sort_by_i32_key(xs, (p: P): i32 => { return p.k; });
    return s[0].k * 100 + s[1].k * 10 + s[2].k;
}
`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "sortbyi32key", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 123 {
		t.Errorf("sort_by_i32_key self-host x86-64 = %d, want 123", code)
	}
}

func TestSelfHostSortByIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sort_by wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range sortByCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.main + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "sort_by_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("sort_by wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
