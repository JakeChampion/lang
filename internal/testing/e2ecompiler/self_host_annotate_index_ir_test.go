package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateIndexCases exercise the ExprIndex.ty carrier (#5531,
// docs/TYPED-IR-REWRITE.md): an index read `a[i]` takes its element width from
// the checker-stamped type, so the value's type and the load width agree.
//
// `if_expr_index_f64` is the shape a structural walk cannot resolve: an index
// whose ARRAY is an if-expression, which desugars to a 0-arg IIFE. Read at the
// wrong width it loads a 4-byte i32 out of an 8-byte-stride f64 array, which
// wasm rejects at validation ("type mismatch: expected f64, found i32"). The
// negative and structural cases pin that the f64 answer reaches nothing else.
//
// These route through the IR emitter; each case ASSERTS "ir" via -decide so a
// change that pushes them off the IR path fails loudly instead of silently
// stopping exercising the carrier. Oracle: the interp.
var annotateIndexCases = []struct {
	name string
	src  string
}{
	// An f64[] produced by an if-expression, then indexed; only the type tag
	// says the element is an f64.
	{"if_expr_index_f64", `function main(): i32 {
    let c: boolean = true;
    let v: f64 = (if (c) { [1.5, 2.5] } else { [3.5, 4.5] })[1];
    return (v * 10.0) as i32;
}`}, // 25
	// Negative guard: the SAME shape over an i32[] must stay 4-byte. A leaf that
	// widened on the tag alone would break this.
	{"if_expr_index_i32", `function main(): i32 {
    let c: boolean = false;
    let v: i32 = (if (c) { [1, 2] } else { [30, 40] })[1];
    return v + 2;
}`}, // 42
	// An f64[] LOCAL indexed.
	{"f64_local_index", `function main(): i32 {
    let xs: f64[] = [1.5, 2.5, 8.25];
    return (xs[2] * 4.0) as i32;
}`}, // 33
	// A call result's struct field indexed — `mk().data[2]` on an f64[] field.
	{"call_struct_field_f64_index", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function main(): i32 { return (mk().data[2] * 10.0) as i32; }`}, // 45
	// The i64 sibling of the case above: an 8-byte integer element reached
	// through the same call-result field.
	{"call_struct_field_i64_index", `struct Box { data: i64[] }
function mk(): Box { return Box { data: [7000000000, 9000000000] }; }
function main(): i32 { return (mk().data[1] / 1000000000) as i32; }`}, // 9
}

// TestSelfHostAnnotateIndexIR_X86_64 runs annotateIndexCases through the
// self-host x86-64 IR path, driven by asm_load_run.fern, which runs
// checker.annotate_module after the checker gate and before emit.
func TestSelfHostAnnotateIndexIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateIndexCases {
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
			progBin := buildBin(t, gcc, dir, "annidx_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
