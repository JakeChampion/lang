package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `let (i, v) = ps[0]` over an UNANNOTATED `(i32, f64)[]` local must type its
// bindings from the element's tuple type (#6165). Read as a 4-byte i32 the f64
// comes back garbage, with the compiler exiting 0 and FERN_STRICT_IR=1 silent.
//
// The controls pin the annotated local and the field read `ps[0].1`, which
// must agree with the destructure one token apart.
var tupleDestructureIndexCases = []struct {
	name string
	src  string
}{
	{"destructure_unannotated_local", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); let (i, v) = ps[0]; return (v * 10.0) as i32 + i; }`}, // 45
	{"destructure_call_index", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let (i, v) = mk()[0]; return (v * 10.0) as i32 + i; }`}, // 45 — no local at all
	{"destructure_i64_element", `function mk(): (i32, i64)[] { return [(5, 4000000000)]; }
function main(): i32 { let (i, v) = mk()[0]; return (v / 100000000) as i32 + i; }`}, // 45
	{"destructure_string_element", `function mk(): (i32, string)[] { return [(40, "abcde")]; }
function main(): i32 { let (i, s) = mk()[0]; return s.len() + i; }`}, // 45
	{"annotated_local_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps: (i32, f64)[] = mk(); let (i, v) = ps[0]; return (v * 10.0) as i32 + i; }`}, // 45 — annotated local
	{"field_read_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { let ps = mk(); return (ps[0].1 * 10.0) as i32; }`}, // 45 — field read, no destructure
}

// TestSelfHostTupleDestructureIndexX86_64 asserts values against the interp
// oracle on the self-host x86-64 backend.
func TestSelfHostTupleDestructureIndexX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range tupleDestructureIndexCases {
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
				t.Errorf("%s = %d, want %d (interp oracle) — a tuple destructure must type its bindings from ExprIndex.ty when no slot tag exists", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostTupleDestructureIndexWasm is the wasm leg.
func TestSelfHostTupleDestructureIndexWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple-destructure index wasm cases")
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

	for _, tc := range tupleDestructureIndexCases {
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
