package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrtupCallBindCases = []struct {
	name string
	src  string
}{
	{"tuple_local_f64", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); let t = ps[0]; return (t.1 * 10.0) as i32; }`},
	{"tuple_local_destructured", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); let t = ps[0]; let (a, b) = t; return (b * 10.0) as i32; }`},
	{"foreach_loop_var", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); let acc: f64 = 0.0; for p in ps { acc = acc + p.1; } return (acc * 10.0) as i32; }`},
	{"tuple_local_i64", `function mk(): (i32, i64)[] { return [(5, 4000000000)]; }
function main(): i32 { let ps = mk(); let t = ps[0]; return (t.1 / 100000000) as i32 + t.0; }`},
	{"direct_index_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); return (ps[0].1 * 10.0) as i32; }`},
	{"annotated_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps: (i32, f64)[] = mk(); let t = ps[0]; return (t.1 * 10.0) as i32; }`},
	{"string_elem_control", `function mk(): (i32, string)[] { return [(5, "abcde")]; }
function main(): i32 { let ps = mk(); let t = ps[0]; return t.1.len() + 40; }`},
	{"literal_control", `function main(): i32 { let ps = [(0, 4.5)]; let t = ps[0]; return (t.1 * 10.0) as i32; }`},
}

func TestSelfHostArrtupCallBindWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-of-tuples call-bind wasm cases")
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

	for _, tc := range arrtupCallBindCases {
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
				t.Errorf("%s = %d, want %d (interp oracle) — an array local bound from a call must carry its element tuple tag", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostArrtupCallBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range arrtupCallBindCases {
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
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
