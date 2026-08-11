package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generic whose return names a type parameter NESTED inside a container —
// `enum2[T](xs: T[]): (i32, T)[]` — reached the call site with `T` unresolved.
//
// The chain, measured rather than inferred (docs/SELFHOST-GENERIC-CALL-ELEMENT-TAG.md):
// `check_call_expr`'s generic-return rule only matched a return type name equal
// to a param's, so `(i32, T)[]` fell through with `T` intact. `type_to_irtag`
// then yields "" for a tuple with an unserialisable element, so `ExprIndex.ty`
// was EMPTY — not wrong — and lowering fell back to an untyped 4-byte read.
// `array.enumerate` at an `f64[]` returned 255 where 45 was expected.
//
// The controls matter here because each of the two ingredients alone was
// already fine: a generic with an ARRAY return (`dup`) and a generic with a
// BARE TUPLE return (`pk`) both worked, as did the NON-generic tuple-array
// (`mk`). Only the combination failed, so a fix that regressed any of the three
// would be trading one gap for another.
var genericNestedRetCases = []struct {
	name string
	src  string
}{
	{"enumerate_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [4.5];
    var ps = array.enumerate(xs);
    var (i, v) = ps[0];
    return (v * 10.0) as i32 + i;
}`}, // 45; was 255 on x86-64 and 0 on wasm
	{"tuple_array_ret_local", `function enum2[T](xs: T[]): (i32, T)[] {
    var out: (i32, T)[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { var xs: f64[] = [4.5]; var ps = enum2(xs); return (ps[0].1 * 10.0) as i32; }`}, // 45; was 255
	{"tuple_array_ret_direct", `function enum2[T](xs: T[]): (i32, T)[] {
    var out: (i32, T)[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { var xs: f64[] = [4.5]; return (enum2(xs)[0].1 * 10.0) as i32; }`}, // 45 — no local at all
	{"tuple_array_ret_string", `function enum2[T](xs: T[]): (i32, T)[] {
    var out: (i32, T)[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { var xs: string[] = ["abcde"]; var (i, s) = enum2(xs)[0]; return s.len() + 40 + i; }`}, // 45
	{"array_ret_control", `function dup[T](xs: T[]): T[] { return [xs[0], xs[0]]; }
function main(): i32 { var xs: f64[] = [4.5]; var ys = dup(xs); return (ys[0] * 10.0) as i32; }`}, // 45 — generic, array return: always worked
	{"bare_tuple_ret_control", `function pk[T](xs: T[]): (i32, T) { return (0, xs[0]); }
function xs_of(): f64[] { return [4.5]; }
function main(): i32 { var t = pk(xs_of()); return (t.1 * 10.0) as i32 + t.0; }`}, // 45 — generic, bare tuple return: always worked
	{"nongeneric_control", `function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); return (ps[0].1 * 10.0) as i32; }`}, // 45 — non-generic tuple-array: always worked
	{"annotated_control", `function enum2[T](xs: T[]): (i32, T)[] {
    var out: (i32, T)[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 { var xs: f64[] = [4.5]; var ps: (i32, f64)[] = enum2(xs); return (ps[0].1 * 10.0) as i32; }`}, // 45 — the annotation gave the slot a concrete arrarr_elem, bypassing the tag
}

// TestSelfHostGenericNestedRetX86_64 asserts values against the interp oracle on
// the self-host x86-64 backend.
func TestSelfHostGenericNestedRetX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
			if out, cerr := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
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
				t.Errorf("%s = %d, want %d (interp oracle) — a generic's return must resolve its type var from the arguments", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostGenericNestedRetWasm is the wasm leg, where the same untyped read
// produced 0 rather than 255.
func TestSelfHostGenericNestedRetWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping generic nested-return wasm cases")
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
			cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
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
