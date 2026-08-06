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
// These cases assert the REFUSAL, not a correct answer: getting `reverse` right
// needs the monomorphisation promotion widened to `T[]`-param shapes, which is
// separate work. A loud failure is the deliverable here — the project's stated
// IR-or-error posture.
var erasedWideArrayRefuseCases = []struct {
	name string
	src  string
}{
	{"reverse_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.reverse(xs);
    return (ys[0] * 10.0) as i32;
}`},
	{"rotate_left_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.rotate_left(xs, 2);
    return (ys[0] * 10.0) as i32;
}`},
	{"drop_f64", `import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.drop(xs, 2);
    return (ys[0] * 10.0) as i32;
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
