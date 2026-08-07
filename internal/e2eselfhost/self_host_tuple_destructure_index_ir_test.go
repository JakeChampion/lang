package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `var (i, v) = ps[0]` over an UNANNOTATED `(i32, f64)[]` local read every
// element as a 4-byte i32, so the f64 came back garbage — exit 255 on the
// self-host x86-64 backend, 0 on wasm, with the compiler exiting 0 and
// FERN_STRICT_IR=1 silent.
//
// The destructure's ExprIndex arm typed its bindings only from a named local's
// recorded `arrarr_elem`, which exists for an ANNOTATED `(tuple)[]` binding.
// `var ps = mk();` records nothing, so the tag walk came back empty and the
// bindings fell to the untyped i32 default. expr_tuple_elem_tag's own ExprIndex
// arm already had the ExprIndex.ty fallback (#6165); the destructure did not.
//
// Which matches the evidence: `ps[0].1` is right while `var (i, v) = ps[0]`
// is wrong, one token apart. The controls below hold that
// line — remove the fallback and only the destructure cases fail.
var tupleDestructureIndexCases = []struct {
	name string
	src  string
}{
	{"destructure_unannotated_local", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); var (i, v) = ps[0]; return (v * 10.0) as i32 + i; }`}, // 45; was 255
	{"destructure_call_index", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var (i, v) = mk()[0]; return (v * 10.0) as i32 + i; }`}, // 45; was 255 — no local at all
	{"destructure_i64_element", `function mk(): (i32, i64)[] { return [(5, 4000000000)]; }
function main(): i32 { var (i, v) = mk()[0]; return (v / 100000000) as i32 + i; }`}, // 45
	{"destructure_string_element", `function mk(): (i32, string)[] { return [(40, "abcde")]; }
function main(): i32 { var (i, s) = mk()[0]; return s.len() + i; }`}, // 45
	{"annotated_local_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps: (i32, f64)[] = mk(); var (i, v) = ps[0]; return (v * 10.0) as i32 + i; }`}, // 45 — arrarr_elem path, always worked
	{"field_read_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); return (ps[0].1 * 10.0) as i32; }`}, // 45 — expr_tuple_elem_tag, already had the fallback
}

// TestSelfHostTupleDestructureIndexX86_64 asserts values against the interp
// oracle on the self-host x86-64 backend.
func TestSelfHostTupleDestructureIndexX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
			if out, cerr := exec.Command(fernBin, "-target", "x86-64", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
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

// TestSelfHostTupleDestructureIndexWasm is the wasm leg, where the same untyped
// read produced 0 rather than 255.
func TestSelfHostTupleDestructureIndexWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple-destructure index wasm cases")
	}
	gcc, _ := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
			cmd := exec.Command(fernBin, "-target", "wasm", mainPath, stdlibRoot, "-o", outWat)
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
