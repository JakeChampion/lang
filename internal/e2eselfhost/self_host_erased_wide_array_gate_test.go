package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Erased-wide `T[]` generics — `reverse[T](xs: T[]): T[]` and friends — index and
// append at an ERASED element width. On the register backends every slot is
// 8 bytes so that is harmless, but on wasm32 an i32 is 4 bytes and an f64 is 8,
// so the copy steps at 4 bytes through an 8-byte-element array.
//
// Before this gate, that produced SILENT WRONG VALUES: `array.reverse` on an
// f64[] returned 1.5 where 4.5 was expected, with the compiler exiting 0 and
// FERN_STRICT_IR=1 reporting nothing. Every other path was correct — native
// interp, native x86-64, and the self-host's own x86-64 backend — so only the
// wasm leg lied.
//
// The erased-wide deferral gate is supposed to keep exactly this off the wasm IR
// path. It missed the shape because it looks for a wide value passed BY VALUE
// through a bare-typevar param; here nothing wide is passed by value at all —
// the erasure is in the element type. Flag '7' (callee_param_is_erased_array)
// closes it.
//
// erasedWideArrayFixedCases used to be the refusal set. The promotion widening
// (clause (c-arr)) clones these per concrete element type, so they now COMPILE
// AND RUN CORRECTLY — a real stride instead of a refusal. Asserting values, not
// routing, is the point: routing said "ir" while returning 1.5 for 4.5.
var erasedWideArrayFixedCases = []struct {
	name string
	src  string
}{
	{"reverse_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.reverse(xs);
    return (ys[0] * 10.0) as i32;
}`}, // 45
	{"rotate_left_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.rotate_left(xs, 2);
    return (ys[0] * 10.0) as i32;
}`}, // 45
	{"drop_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.drop(xs, 2);
    return (ys[0] * 10.0) as i32;
}`}, // 45
}

// erasedWideArrayRefuseCases keep the GATE honest after the promotion landed.
// The promotion is guarded to single-typevar generics, so a two-typevar
// `map[T, U](xs: T[], f)` at a wide element type is still erased — and must
// still be REFUSED rather than miscompiled. Without a case here the gate could
// rot silently and this class would go back to returning wrong numbers.
var erasedWideArrayRefuseCases = []struct {
	name string
	src  string
}{
	{"map_f64_two_typevars", `import "std/array";
function dbl(x: f64): f64 { return x * 2.0; }
function main(): i32 {
    var xs: f64[] = [1.5, 2.25];
    var ys: f64[] = array.map(xs, dbl);
    return (ys[1] * 10.0) as i32;
}`},
}

// erasedWideArrayAllowCases must STILL lower: the gate keys on a wide ELEMENT,
// so a narrow-element instantiation of the very same generic is unaffected. A
// gate that refused these would be too wide and would cost real coverage.
var erasedWideArrayAllowCases = []struct {
	name string
	src  string
}{
	{"reverse_i32", `import "std/array";
function main(): i32 {
    var xs: i32[] = [1, 2, 4];
    var ys: i32[] = array.reverse(xs);
    return ys[0] * 10 + 5;
}`}, // 45
	{"reverse_string", `import "std/array";
function main(): i32 {
    var xs: string[] = ["ab", "cdef"];
    var ys: string[] = array.reverse(xs);
    return ys[0].len() + 38;
}`}, // 42
}

// erasedWideArrayBlindCases are callees that take an erased `T[]` but never read
// an ELEMENT — so no stride is ever used and there is nothing to miscompile. The
// gate used to refuse them anyway, because it asked only whether a wide-element
// array reached an erased `T[]` param. `is_empty` is the one such shape in the
// whole of std/array (the other concrete-return generics — `all`, `any`,
// `count_where`, `position`, `sum_by` — feed elements to a predicate and stay
// refused, correctly).
var erasedWideArrayBlindCases = []struct {
	name string
	src  string
}{
	{"is_empty_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5];
    if (array.is_empty(xs)) { return 1; }
    return 45;
}`}, // 45
	{"len_only_generic_f64", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    return count_of(xs) * 20 + 5;
}`}, // 45
	{"len_only_generic_i64", `function count_of[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 {
    var xs: i64[] = [9000000000, 1];
    return count_of(xs) * 20 + 5;
}`}, // 45
}

// TestSelfHostErasedWideArrayGateBlindWasm pins the other edge of the gate: a
// callee that only asks its erased array for `.len()` must still lower and run.
// Paired with TestSelfHostErasedWideArrayGateWasm — that test fails if the gate
// gets too narrow, this one if it gets too wide, and neither alone is enough.
func TestSelfHostErasedWideArrayGateBlindWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide element-blind wasm cases")
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
			cmd := exec.Command(fernBin, "-target", "wasm", mainPath, stdlibRoot, "-o", outWat)
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

// TestSelfHostErasedWideArrayGateWasm asserts a wide-element instantiation of a
// `T[]`-param generic is REFUSED on the wasm IR path rather than miscompiled.
func TestSelfHostErasedWideArrayGateWasm(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	// fern.fern — the real CLI — because these programs `import "std/array"`, and
	// a driver without a loader silently ignores the import and then reports a
	// verdict about a broken program (the warning in CLAUDE.md's probing note).
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range erasedWideArrayRefuseCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			cmd := exec.Command(fernBin, "-target", "wasm", mainPath, stdlibRoot, "-o", outWat)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if !strings.Contains(stderr.String(), "not IR-eligible") {
				t.Errorf("%s: wanted a refusal naming IR-ineligibility, got stderr %q — a wide-element T[] generic must NOT reach the wasm IR path (it miscompiles to a 4-byte stride)", tc.name, stderr.String())
			}
		})
	}
}

// TestSelfHostErasedWideArrayFixedWasm pins that the promoted shapes now produce
// the RIGHT ANSWER on wasm, against the interp oracle.
func TestSelfHostErasedWideArrayFixedWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide promoted wasm cases")
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

	for _, tc := range erasedWideArrayFixedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			if out, cerr := exec.Command(fernBin, "-target", "wasm", mainPath, stdlibRoot, "-o", outWat).CombinedOutput(); cerr != nil {
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

// TestSelfHostErasedWideArrayGateNarrowWasm pins that the gate keys on element
// WIDTH: the same generics at i32 / string elements must still lower and run.
func TestSelfHostErasedWideArrayGateNarrowWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide narrow-element wasm gate")
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

	for _, tc := range erasedWideArrayAllowCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			if out, cerr := exec.Command(fernBin, "-target", "wasm", mainPath, stdlibRoot, "-o", outWat).CombinedOutput(); cerr != nil {
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
