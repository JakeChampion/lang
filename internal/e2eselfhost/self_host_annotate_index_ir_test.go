package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateIndexCases exercise the ExprIndex.ty typed-IR carrier (#5531's third,
// docs/TYPED-IR-REWRITE.md). An index read `a[i]` yields an ELEMENT whose width
// irlower must know at two places that have to agree: the value predicates
// (expr_is_f64 / infer_expr_width, which decide how the result is typed
// downstream) and the load site (lower_expr's arr_get width / lower_i64's
// arr_get_i64, which decide how many bytes come out of memory). Both now read
// one leaf, ix_type_tag, which prefers the structural walk and falls back to the
// checker-stamped ix.ty.
//
// The gap that motivated it: an index whose ARRAY is an if-EXPRESSION. That
// desugars to a 0-arg IIFE — an ExprCall with a LAMBDA callee — and
// arr_index_is_f64's ExprCall arm only recognises an ExprIdent or
// ExprFieldAccess callee, so it answered false for an f64[] the checker had
// typed all along. The read then loaded a 4-byte i32 out of an 8-byte-stride
// f64 array. Measured on the pre-carrier compiler, `if_expr_index_f64` below
// emitted wasm the validator rejects outright ("type mismatch: expected f64,
// found i32") rather than a wrong number — so it exits 1 against an oracle of
// 25. The negative and structural cases pin that the leaf did not widen
// anything else.
//
// These route through the IR emitter; each case ASSERTS "ir" via -decide so a
// change that pushes them off the IR path fails loudly instead of silently
// stopping exercising the carrier. Oracle: the interp.
var annotateIndexCases = []struct {
	name string
	src  string
}{
	// The gap: an f64[] produced by an if-expression, then indexed. Pre-carrier
	// this emitted invalid wasm; the tag supplies the f64 the walk could not.
	{"if_expr_index_f64", `function main(): i32 {
    var c: boolean = true;
    var v: f64 = (if (c) { [1.5, 2.5] } else { [3.5, 4.5] })[1];
    return (v * 10.0) as i32;
}`}, // 25
	// Negative guard: the SAME shape over an i32[] must stay 4-byte. A leaf that
	// widened on the tag alone would break this.
	{"if_expr_index_i32", `function main(): i32 {
    var c: boolean = false;
    var v: i32 = (if (c) { [1, 2] } else { [30, 40] })[1];
    return v + 2;
}`}, // 42
	// Structural path, unchanged: an f64[] LOCAL indexed. The walk resolves this
	// from the slot, so ix_type_tag must answer before ever consulting the tag.
	{"f64_local_index", `function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 8.25];
    return (xs[2] * 4.0) as i32;
}`}, // 33
	// Structural path via a call result's struct field — `mk().data[2]` on an
	// f64[] field, which expr_struct_type recovers. Guards the arm the tag now
	// sits behind rather than in front of.
	{"call_struct_field_f64_index", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function main(): i32 { return (mk().data[2] * 10.0) as i32; }`}, // 45
	// The i64 sibling of the case above: an 8-byte integer element reached
	// through the same structural arm, pinning that lower_i64's ExprIndex arm
	// still takes the arr_get_i64 path after being routed through ix_type_tag.
	{"call_struct_field_i64_index", `struct Box { data: i64[] }
function mk(): Box { return Box { data: [7000000000, 9000000000] }; }
function main(): i32 { return (mk().data[1] / 1000000000) as i32; }`}, // 9
}

// TestSelfHostAnnotateIndexIR_X86_64 pins the ExprIndex.ty carrier feeding
// irlower's ix_type_tag through the self-host x86-64 IR path. asm_load_run.fern
// is the driver because it runs checker.annotate_module after the checker gate
// and before emit — asm_ir_run and the native compiler skip the pass, leaving
// every ty empty and exercising only the structural walk.
func TestSelfHostAnnotateIndexIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateIndexCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annidx_"+tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
