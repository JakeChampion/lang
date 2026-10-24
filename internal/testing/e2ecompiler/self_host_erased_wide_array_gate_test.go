package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var erasedWideArrayFixedCases = []struct {
	name string
	src  string
}{
	{"flat_map_method_i64", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: i64[] = xs.flat_map((x: i32) => [(x as i64) * 2i64]);
    if (w[2] != 6i64) { return 2; }
    return 11;
}`},
	{"flat_map_method_f64", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: f64[] = xs.flat_map((x: i32) => [(x as f64) * 1.5]);
    if (w[2] < 4.4) { return 2; }
    return 11;
}`},
	{"reverse_f64", `import "std/array";
function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 4.5];
    let ys: f64[] = array.reverse(xs);
    return (ys[0] * 10.0) as i32;
}`},
	{"rotate_left_f64", `import "std/array";
function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 4.5];
    let ys: f64[] = array.rotate_left(xs, 2);
    return (ys[0] * 10.0) as i32;
}`},
	{"drop_f64", `import "std/array";
function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 4.5];
    let ys: f64[] = array.drop(xs, 2);
    return (ys[0] * 10.0) as i32;
}`},
	{"map_f64_two_typevars", `import "std/array";
function dbl(x: f64): f64 { return x * 2.0; }
function main(): i32 {
    let xs: f64[] = [1.5, 2.25];
    let ys: f64[] = array.map(xs, dbl);
    return (ys[1] * 10.0) as i32;
}`},
	{"map_method_i64", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: i64[] = xs.map((x: i32) => (x as i64) * 2i64);
    if (w[2] != 6i64) { return 2; }
    return 11;
}`},
	{"map_method_i64_return", `import "std/array";
function g(xs: i32[]): i64[] { return xs.map((x: i32) => (x as i64) * 2i64); }
function main(): i32 {
    let w: i64[] = g([1, 2, 3]);
    if (w[2] != 6i64) { return 2; }
    return 11;
}`},
	{"map_method_i64_assign", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: i64[] = [0i64];
    w = xs.map((x: i32) => (x as i64) * 2i64);
    if (w[2] != 6i64) { return 2; }
    return 11;
}`},
	{"map_method_i64_elem_named", `import "std/array";
function dbl(x: i64): i64 { return x * (2 as i64); }
function twice(own xs: i64[]): i64[] { return xs.map(dbl); }
function main(): i32 { return twice([3 as i64, 5 as i64]).len() + 40; }`},
	{"map_method_i64_elem_lambda", `import "std/array";
function main(): i32 {
    let xs: i64[] = [3 as i64, 5 as i64];
    let ys: i64[] = xs.map((x: i64): i64 => x * (2 as i64));
    return (ys[0] + ys[1]) as i32;
}`},
	{"map_method_i64_elem_narrow_result", `import "std/array";
function main(): i32 {
    let xs: i64[] = [3 as i64, 5 as i64];
    let ys: i32[] = xs.map((x: i64): i32 => (x as i32) + 1);
    return ys[0] * 10 + ys[1];
}`},
	{"map_method_f64_elem_named", `import "std/array";
function dbl(x: f64): f64 { return x * 2.0; }
function main(): i32 {
    let xs: f64[] = [1.5, 2.25];
    let ys: f64[] = xs.map(dbl);
    return (ys[1] * 10.0) as i32;
}`},
}

var erasedWideArrayAllowCases = []struct {
	name string
	src  string
}{
	{"reverse_i32", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 4];
    let ys: i32[] = array.reverse(xs);
    return ys[0] * 10 + 5;
}`},
	{"reverse_string", `import "std/array";
function main(): i32 {
    let xs: string[] = ["ab", "cdef"];
    let ys: string[] = array.reverse(xs);
    return ys[0].len() + 38;
}`},
	{"map_method_i32", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: i32[] = xs.map((x: i32) => x * 2);
    return w[2] * 7 + 3;
}`},
	{"map_method_string", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: string[] = xs.map((x: i32) => "abcde");
    return w[2].len() + 40;
}`},
	{"flat_map_method_i32", `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let w: i32[] = xs.flat_map((x: i32) => [x * 2]);
    return w[2] * 7 + 3;
}`},
}

var erasedWideArrayBlindCases = []struct {
	name string
	src  string
}{
	{"is_empty_f64", `import "std/array";
function main(): i32 {
    let xs: f64[] = [1.5, 2.5];
    if (array.is_empty(xs)) { return 1; }
    return 45;
}`},
	{"len_only_generic_f64", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    let xs: f64[] = [4.5, 1.5];
    return count_of(xs) * 20 + 5;
}`},
	{"len_only_generic_i64", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    let xs: i64[] = [9000000000, 1];
    return count_of(xs) * 20 + 5;
}`},
}

func TestSelfHostErasedWideArrayGateBlindWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide element-blind wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedWideArrayBlindCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s) — a callee that never reads an element uses no stride, so the gate must not refuse it", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostErasedWideArrayFixedWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide promoted wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedWideArrayFixedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — the promoted clone must copy at the concrete element stride", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostErasedWideArrayGateNarrowWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide narrow-element wasm gate")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedWideArrayAllowCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — the gate must key on element WIDTH, not on the generic", tc.name, got, want)
			}
		})
	}
}
