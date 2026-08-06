package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An array-of-tuples local bound from a CALL (`var ps = mk()`) recorded no
// `arrarr_elem`, because that slot tag was only derived from an ANNOTATION or an
// array LITERAL. Every consumer that reads a tuple element tag off the slot then
// fell through to an untyped 4-byte read.
//
// That made it look like a width bug rather than a missing tag: on wasm32 an f64
// element came back wrong while a string element — a 4-byte pointer — survived.
// The register backends give every slot 8 bytes and lost nothing, so only the
// wasm leg lied. See docs/SELFHOST-TUPLE-ARRAY-LOCAL-TAGS.md.
//
// Fixed at the SOURCE (arrtup_ret_fns / arrtup_ret_type populate the slot at the
// binding) rather than per-consumer. Four sites read this tag; two already had an
// `ExprIndex.ty` fallback (#6165, #6279) and two did not — and one of those,
// `for p in ps`, has no ExprIndex node, so a third copy of the fallback could not
// have reached it. `foreach_loop_var` is the case that proves the difference.
var arrtupCallBindCases = []struct {
	name string
	src  string
}{
	{"tuple_local_f64", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); var t = ps[0]; return (t.1 * 10.0) as i32; }`}, // 45; was 1 on wasm
	{"tuple_local_destructured", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); var t = ps[0]; var (a, b) = t; return (b * 10.0) as i32; }`}, // 45; was 0 on wasm
	{"foreach_loop_var", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); var acc: f64 = 0.0; for p in ps { acc = acc + p.1; } return (acc * 10.0) as i32; }`}, // 45; was 1 on wasm — no ExprIndex node, so only the upstream fix reaches it
	{"tuple_local_i64", `function mk(): (i32, i64)[] { return [(5, 4000000000)]; }
function main(): i32 { var ps = mk(); var t = ps[0]; return (t.1 / 100000000) as i32 + t.0; }`}, // 45
	{"direct_index_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); return (ps[0].1 * 10.0) as i32; }`}, // 45 — no intermediate local: always worked
	{"annotated_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps: (i32, f64)[] = mk(); var t = ps[0]; return (t.1 * 10.0) as i32; }`}, // 45 — annotation supplied arrarr_elem: always worked
	{"string_elem_control", `function mk(): (i32, string)[] { return [(5, "abcde")]; }
function main(): i32 { var ps = mk(); var t = ps[0]; return t.1.len() + 40; }`}, // 45 — a 4-byte pointer survived the untyped read even before the fix
	{"literal_control", `function main(): i32 { var ps = [(0, 4.5)]; var t = ps[0]; return (t.1 * 10.0) as i32; }`}, // 45 — the literal arm already inferred the tag
}

// TestSelfHostArrtupCallBindWasm is the leg the bug was on.
func TestSelfHostArrtupCallBindWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping array-of-tuples call-bind wasm cases")
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
				t.Errorf("%s = %d, want %d (interp oracle) — an array local bound from a call must carry its element tuple tag", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostArrtupCallBindX86_64 pins that the register backend, which was
// already correct because its slots are 8 bytes wide, stays correct.
func TestSelfHostArrtupCallBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
