package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var erasedElemReturnCases = []struct {
	name string
	src  string
}{
	{"first_f64", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let xs: f64[] = [4.5, 1.5];
    return (first_of(xs) * 10.0) as i32;
}`},
	{"first_i64", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let xs: i64[] = [9000000000, 1];
    return (first_of(xs) / 1000000000) as i32 + 36;
}`},
	{"first_string", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let xs: string[] = ["abcde"];
    return first_of(xs).len() + 40;
}`},
	{"first_i32", `function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let xs: i32[] = [4, 1];
    return first_of(xs) * 10 + 5;
}`},
	{"first_struct", `struct P { a: i32 }
function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    let xs: P[] = [P { a: 4 }, P { a: 1 }];
    return first_of(xs).a * 10 + 5;
}`},
	{"pair_of_f64", `function pair_of[T](xs: T[]): (T, T) { return (xs[0], xs[1]); }
function main(): i32 {
    let xs: f64[] = [4.5, 1.5];
    let (a, b) = pair_of(xs);
    return ((a + b) * 10.0) as i32;
}`},
	{"pair_of_string", `function pair_of[T](xs: T[]): (T, T) { return (xs[0], xs[1]); }
function main(): i32 {
    let xs: string[] = ["ab", "cde"];
    let (a, b) = pair_of(xs);
    return a.len() + b.len() + 40;
}`},
	{"mixed_tuple_return", `function head_and_one[T](xs: T[]): (T, i32) { return (xs[0], 1); }
function main(): i32 {
    let xs: f64[] = [4.4];
    let (a, b) = head_and_one(xs);
    return (a * 10.0) as i32 + b;
}`},
	{"receiver_typevar_control", `struct Pair { a: i32 }
function (xs: T[]) firstpair[U](other: U[]): (T, U) { return (xs[0], other[0]); }
function main(): i32 {
    let a: f64[] = [4.5];
    let b: i32[] = [1];
    let (x, y) = a.firstpair(b);
    return (x * 10.0) as i32 + y - 1;
}`},
	{"count_of_control", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    let xs: f64[] = [4.5, 1.5];
    return count_of(xs) * 20 + 5;
}`},
	{"id_of_control", `function id_of[T](x: T): T { return x; }
function main(): i32 {
    let xs: f64[] = [4.5, 1.5];
    return (id_of(xs[0]) * 10.0) as i32;
}`},
}

func TestSelfHostErasedElemReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedElemReturnCases {
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
				t.Errorf("%s = %d, want %d (interp oracle) — an erased T[] param with a bare T return must monomorphise so the call site knows the result type", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostErasedElemReturnWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-element-return wasm cases")
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

	for _, tc := range erasedElemReturnCases {
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
