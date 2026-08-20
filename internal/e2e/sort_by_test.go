package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/sort's generic comparator sort — `sort_by[T](arr, cmp)` / `is_sorted_by`.
// The comparator is a `(T, T) => i32` closure called in a `while` CONDITION, so
// these exercise the lift-fn-args-in-conditions path (#2686 tail) on a bounded-
// generic body. Lower on every native backend AND the self-host IR path.
var sortByCases = []struct {
	name string
	main string
	want int
}{
	// descending i32 sort via a comparator closure → [9,5,2,1]; 9*10+1 = 91.
	{"sort-by-desc", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { var out: T[] = arr; var n = out.len(); var i = 1; while (i < n) { var j = i; while (j > 0 && cmp(out[j], out[j - 1]) < 0) { var tmp: T = out[j]; out = out.with(j, out[j - 1]); out = out.with(j - 1, tmp); j = j - 1; } i = i + 1; } return out; }
function main(): i32 { var xs: i32[] = [5, 1, 9, 2]; var s = sort_by(xs, function (a: i32, b: i32): i32 { if (a > b) { return 0 - 1; } if (a < b) { return 1; } return 0; }); return s[0] * 10 + s[3]; }`, 91},
	// ascending i32 sort via a comparator closure → [1,2,3]; 1*100+2*10+3 = 123.
	{"sort-by-asc", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { var out: T[] = arr; var n = out.len(); var i = 1; while (i < n) { var j = i; while (j > 0 && cmp(out[j], out[j - 1]) < 0) { var tmp: T = out[j]; out = out.with(j, out[j - 1]); out = out.with(j - 1, tmp); j = j - 1; } i = i + 1; } return out; }
function main(): i32 { var xs: i32[] = [3, 1, 2]; var s = sort_by(xs, function (a: i32, b: i32): i32 { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	// is_sorted_by under an ascending comparator. sorted→+5, unsorted→+2 → 7.
	{"is-sorted-by", `function is_sorted_by[T](arr: T[], cmp: (T, T) => i32): boolean { var i = 1; var n = arr.len(); while (i < n) { if (cmp(arr[i], arr[i - 1]) < 0) { return false; } i = i + 1; } return true; }
function asc(a: i32, b: i32): i32 { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }
function main(): i32 { var a: i32[] = [1, 2, 3]; var b: i32[] = [3, 1]; var r = 0; if (is_sorted_by(a, asc)) { r = r + 5; } if (!is_sorted_by(b, asc)) { r = r + 2; } return r; }`, 7},
	// The shipped `sort_by` body is a stable bottom-up merge sort (#4387 item 3),
	// not insertion sort — verify its exact shape lowers + runs on every backend
	// AND the self-host IR path over a 10-element input with duplicates:
	// [8,3,3,9,1,7,2,5,0,6] asc → [0,1,2,3,3,5,6,7,8,9]; s[0]*100+s[5]*10+s[9] =
	// 0 + 50 + 9 = 59. Larger than the min two-run case, so it drives the
	// width=1→2→4→8 pass loop and the tail (odd, short) runs.
	{"sort-by-merge", `function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] { var n = arr.len(); if (n < 2) { return arr; } var src: T[] = arr; var width = 1; while (width < n) { var dst: T[] = src; var lo = 0; while (lo < n) { var mid = lo + width; if (mid > n) { mid = n; } var hi = lo + width + width; if (hi > n) { hi = n; } var i = lo; var j = mid; var k = lo; while (i < mid && j < hi) { if (cmp(src[j], src[i]) < 0) { dst = dst.with(k, src[j]); j = j + 1; } else { dst = dst.with(k, src[i]); i = i + 1; } k = k + 1; } while (i < mid) { dst = dst.with(k, src[i]); i = i + 1; k = k + 1; } while (j < hi) { dst = dst.with(k, src[j]); j = j + 1; k = k + 1; } lo = lo + width + width; } src = dst; width = width + width; } return src; }
function main(): i32 { var xs: i32[] = [8, 3, 3, 9, 1, 7, 2, 5, 0, 6]; var s = sort_by(xs, function (a: i32, b: i32): i32 { if (a < b) { return 0 - 1; } if (a > b) { return 1; } return 0; }); return s[0] * 100 + s[5] * 10 + s[9]; }`, 59},
}

// TestNativeSortBy runs the inline sort_by programs on interp / x86-64 / wasm.
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

// TestNativeSortByArm64 is the arm64 leg (CI-gated; qemu).
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

// TestNativeSortByModule exercises the shipped `import "std/sort"` sort_by /
// is_sorted_by over an i32 array with a descending comparator.
func TestNativeSortByModule(t *testing.T) {
	src := `import "std/sort" as sort;
function desc(a: i32, b: i32): i32 { if (a > b) { return 0 - 1; } if (a < b) { return 1; } return 0; }
function main(): i32 {
    var s = sort.sort_by([5, 1, 9, 2], desc); // [9,5,2,1]
    var ok = 0;
    if (sort.is_sorted_by(s, desc)) { ok = 1; } // 1
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

// TestSelfHostSortByIRX86_64 drives the inline cases through the self-hosted
// x86-64 compiler and oracle-checks the exit code.
func TestSelfHostSortByIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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

// sortByI32KeyProg sorts a struct array by an i32-key projection (closure
// `(P) => i32`), the common "sort records by a numeric field" case. Keys are
// precomputed once (Schwartzian transform). [k:3, k:1, k:2] → [1,2,3];
// 1*100 + 2*10 + 3 = 123.
const sortByI32KeyProg = `import "std/sort" as sort;
struct P { k: i32, tag: i32 }
function main(): i32 {
    var xs: P[] = [P { k: 3, tag: 0 }, P { k: 1, tag: 0 }, P { k: 2, tag: 0 }];
    var s = sort.sort_by_i32_key(xs, function (p: P): i32 { return p.k; });
    return s[0].k * 100 + s[1].k * 10 + s[2].k;
}
`

// TestNativeSortByI32Key exercises the shipped `import "std/sort"`
// `sort_by_i32_key` on interp / x86-64 / wasm.
func TestNativeSortByI32Key(t *testing.T) {
	p := writeIterProg(t, sortByI32KeyProg)
	if _, code := runFixtureInterp(t, p, ""); code != 123 {
		t.Errorf("sort_by_i32_key interp = %d, want 123", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 123 {
		t.Errorf("sort_by_i32_key x86-64 = %d, want 123", code)
	}
	if code := runWasm(t, sortByI32KeyProg); code != 123 {
		t.Errorf("sort_by_i32_key wasm = %d, want 123", code)
	}
}

// TestNativeSortByI32KeyArm64 is the arm64 leg (CI-gated; qemu).
func TestNativeSortByI32KeyArm64(t *testing.T) {
	p := writeIterProg(t, sortByI32KeyProg)
	if _, code := runFixtureArm64(t, p, ""); code != 123 {
		t.Errorf("sort_by_i32_key arm64 = %d, want 123", code)
	}
}

// TestSelfHostSortByI32Key drives `sort_by_i32_key` through the self-hosted
// x86-64 compiler and oracle-checks the result. It asserts BEHAVIOUR, not the
// routing tag: a closure-typed param over a generic `T[]` currently lowers via
// the AST emitter rather than the IR path on the self-host, which is still
// correct end-to-end (the legitimate fallback). IR-eligibility for this shape
// is a self-host codegen follow-up.
func TestSelfHostSortByI32Key(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	// Inline the helper (the self-host single-program driver doesn't resolve
	// `import "std/sort"`); same body as the shipped sort_by_i32_key.
	prog := `struct P { k: i32, tag: i32 }
function sort_by_i32_key[T](arr: T[], key: (T) => i32): T[] {
    var out: T[] = arr;
    var keys: i32[] = [];
    for x in out { keys = keys.append(key(x)); }
    var n: i32 = out.len();
    var i: i32 = 1;
    while (i < n) {
        var j: i32 = i;
        while (j > 0 && keys[j] < keys[j - 1]) {
            var tv: T = out[j];
            out = out.with(j, out[j - 1]);
            out = out.with(j - 1, tv);
            var tk: i32 = keys[j];
            keys = keys.with(j, keys[j - 1]);
            keys = keys.with(j - 1, tk);
            j = j - 1;
        }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    var xs: P[] = [P { k: 3, tag: 0 }, P { k: 1, tag: 0 }, P { k: 2, tag: 0 }];
    var s = sort_by_i32_key(xs, function (p: P): i32 { return p.k; });
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

// TestSelfHostSortByIRWasm is the wasm leg.
func TestSelfHostSortByIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host sort_by wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range sortByCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
