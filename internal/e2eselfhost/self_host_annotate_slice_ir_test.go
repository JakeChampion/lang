package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateSliceCases exercise the ExprSlice.ty carrier — the fourth in the
// Phase-A annotate-and-consume migration (docs/TYPED-IR-REWRITE.md), after
// ExprCall.ty (#5531), ExprFieldAccess.ty (#5986) and ExprIndex.ty (#6165).
//
// A slice `a[i:j]` asks irlower two questions about its SOURCE array, and the
// structural walk misses both whenever the source is not a named slot:
//
//   - Is it an array at all? `expr_is_arr_src` gates the whole lowering, and a
//     source it cannot recognise bails the module off the IR path. It takes a
//     bare Expr and so cannot consult the tag itself, which is why the tag is
//     applied at the one site holding the ExprSlice node.
//   - Are its elements 8-byte? `slice_elem_is_wide` picks the arr_slice width;
//     getting it wrong copies 4-byte elements out of an 8-byte array.
//
// Both are load-bearing here. The first three cases BAILED before the carrier
// (verified against the pre-carrier compiler); `slice_of_call_field_i64`
// additionally needs the width half — with only the gate wired it lowers at a
// 4-byte stride and returns the wrong number rather than failing.
//
// Cases route through the IR emitter and ASSERT "ir" via -decide, so a change
// that pushes them off the IR path fails loudly instead of silently ceasing to
// exercise the carrier. Oracle: the interp.
var annotateSliceCases = []struct {
	name string
	src  string
}{
	// Gap 1: the source is an if-expression (a 0-arg IIFE after desugar), which
	// expr_is_arr_src does not recognise. Bailed pre-carrier.
	{"slice_of_if_expr_f64", `function main(): i32 {
    var c: boolean = true;
    var xs: [f64] = (if (c) { [1.5, 2.5, 4.5] } else { [3.5, 4.5, 5.5] })[0:2];
    return (xs[1] * 10.0) as i32;
}`}, // 25
	// Gap 2: the source is a struct field reached through a call result. Bailed
	// pre-carrier.
	{"slice_of_call_field_f64", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function main(): i32 {
    var xs: [f64] = mk().data[0:2];
    return (xs[1] * 10.0) as i32;
}`}, // 25
	// Gap 3: the i64 sibling — exercises BOTH halves, since the element width
	// must also come from the tag. A 4-byte stride here returns a wrong value.
	{"slice_of_call_field_i64", `struct Box { data: i64[] }
function mk(): Box { return Box { data: [7000000000, 9000000000, 1] }; }
function main(): i32 {
    var xs: [i64] = mk().data[0:2];
    return (xs[1] / 1000000000) as i32;
}`}, // 9
	// Negative guard: the same if-expression shape over an i32[] must stay
	// 4-byte. A leaf that widened on any non-empty tag would break this.
	{"slice_of_if_expr_i32_narrow", `function main(): i32 {
    var c: boolean = true;
    var xs: [i32] = (if (c) { [10, 20, 30] } else { [40, 50, 60] })[0:2];
    return xs[1] + 22;
}`}, // 42
	// Negative guard: a string[] element is pointer-shaped, not 8-byte-numeric.
	{"slice_of_call_field_string", `struct Box { data: string[] }
function mk(): Box { return Box { data: ["ab", "cd", "ef"] }; }
function main(): i32 {
    var xs: [string] = mk().data[0:2];
    return xs[1].len() + 40;
}`}, // 42
	// Structural path, unchanged: a slice of a named f64[] local. The walk
	// resolves this from the slot, so the tag must never be consulted.
	{"slice_of_local_f64", `function main(): i32 {
    var a: f64[] = [1.5, 2.5, 4.5];
    var xs: [f64] = a[0:2];
    return (xs[1] * 10.0) as i32;
}`}, // 25
	// Structural path, unchanged: a slice of a nested array element `m[1][0:2]`.
	{"slice_of_nested_index_f64", `function main(): i32 {
    var m: f64[][] = [[1.5, 2.5, 4.5], [9.5, 8.5, 7.5]];
    var xs: [f64] = m[1][0:2];
    return (xs[1] * 10.0) as i32;
}`}, // 85
}

// TestSelfHostAnnotateSliceIR_X86_64 pins the ExprSlice.ty carrier through the
// self-host x86-64 IR path. asm_load_run.fern is the driver because it runs
// checker.annotate_module; a driver that skips annotation leaves every ty empty
// and cannot regress.
func TestSelfHostAnnotateSliceIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateSliceCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annslice_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
