package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var genericNestedRetCases = []struct {
	name string
	src  string
}{
	{"enumerate_f64", `import "std/array";
function main(): i32 {
    let xs: f64[] = [4.5];
    let ps = array.enumerate(xs);
    let (i, v) = ps[0];
    return (v * 10.0) as i32 + i;
}`},
	{"tuple_array_ret_local", `function enum2[T](xs: T[]): (i32, T)[] {
    let out: (i32, T)[] = [];
    let i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { let xs: f64[] = [4.5]; let ps = enum2(xs); return (ps[0].1 * 10.0) as i32; }`},
	{"tuple_array_ret_direct", `function enum2[T](xs: T[]): (i32, T)[] {
    let out: (i32, T)[] = [];
    let i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { let xs: f64[] = [4.5]; return (enum2(xs)[0].1 * 10.0) as i32; }`},
	{"tuple_array_ret_string", `function enum2[T](xs: T[]): (i32, T)[] {
    let out: (i32, T)[] = [];
    let i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { let xs: string[] = ["abcde"]; let (i, s) = enum2(xs)[0]; return s.len() + 40 + i; }`},
	{"array_ret_control", `function dup[T](xs: T[]): T[] { return [xs[0], xs[0]]; }
function main(): i32 { let xs: f64[] = [4.5]; let ys = dup(xs); return (ys[0] * 10.0) as i32; }`},
	{"bare_tuple_ret_control", `function pk[T](xs: T[]): (i32, T) { return (0, xs[0]); }
function xs_of(): f64[] { return [4.5]; }
function main(): i32 { let t = pk(xs_of()); return (t.1 * 10.0) as i32 + t.0; }`},
	{"nongeneric_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); return (ps[0].1 * 10.0) as i32; }`},
	{"annotated_control", `function enum2[T](xs: T[]): (i32, T)[] {
    let out: (i32, T)[] = [];
    let i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { let xs: f64[] = [4.5]; let ps: (i32, f64)[] = enum2(xs); return (ps[0].1 * 10.0) as i32; }`},
}

func TestSelfHostGenericNestedRetX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range genericNestedRetCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			var rcmd *exec.Cmd
			if len(runner) == 0 {
				rcmd = exec.Command(binPath)
			} else {
				rcmd = exec.Command(runner[0], append(runner[1:], binPath)...)
			}
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a generic's return must resolve its type let from the arguments", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostGenericNestedRetWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping generic nested-return wasm cases")
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

	for _, tc := range genericNestedRetCases {
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
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
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
