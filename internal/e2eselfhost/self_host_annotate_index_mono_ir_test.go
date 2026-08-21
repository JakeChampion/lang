package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateIndexMonoCases pin that the ExprIndex.ty carrier (#6165) SURVIVES
// monomorphisation. `mono_expr` / `ms_expr` / `me_expr` (parser.fern) rebuild
// every expression when cloning a generic function; they used to rebuild an
// ExprIndex with `ty: ""`, so a clone lost the tag and irlower's ix_type_tag
// fell back to the structural walk — which misses exactly the shapes the tag
// exists to cover.
//
// That is not a missed optimisation, it is a miscompile, and it is invisible to
// the path probe: the module still routes "ir" and the compiler still exits 0.
// Measured before the fix, `generic_if_expr_index_f64` below emitted wasm whose
// CLONE (`pick__i32`) the validator rejects — "type mismatch: expected f64,
// found i32" at the f64 element read — exiting 1 against an oracle of 25.
//
// The asymmetry the fix encodes: these three sites also drop `unchecked`, and
// that stays dropped. Losing the bounds-elide mark is conservative (the clone
// keeps its bounds check); losing the type tag is not.
//
// Single instantiation per case is deliberate: instantiating one of these
// generics TWICE trips a pre-existing native monomorph re-check bug (it binds T
// from the return type, so a second call with a different T fails "expected
// f64, got boolean"), which is unrelated to the carrier and would make the case
// test the oracle rather than the compiler.
var annotateIndexMonoCases = []struct {
	name string
	src  string
}{
	// The gap: an f64 if-expression index inside a generic body. The clone must
	// carry the tag or it reads the 8-byte element as a 4-byte i32.
	{"generic_if_expr_index_f64", `function pick[T](flag: boolean, t: T): f64 {
    return (if (flag) { [1.5, 2.5] } else { [3.5, 4.5] })[1];
}
function main(): i32 {
    return (pick(true, 7) * 10.0) as i32;
}`}, // 25
	// A structural-path index inside a generic body — the walk resolves this one
	// on its own, so it guards that carrying the tag did not disturb the shapes
	// that already worked.
	{"generic_call_struct_field_index", `struct Box { data: f64[] }
function mk(): Box { return Box { data: [1.5, 2.5, 4.5] }; }
function grab[T](t: T): f64 { return mk().data[2]; }
function main(): i32 { return (grab(3) * 10.0) as i32; }`}, // 45
	// Negative guard: the same if-expression shape over an i32[] inside a
	// generic must NOT widen just because a tag now survives cloning.
	{"generic_if_expr_index_i32", `function idx[T](flag: boolean, t: T): i32 {
    return (if (flag) { [1, 2] } else { [30, 40] })[1];
}
function main(): i32 { return idx(false, 9) + 2; }`}, // 42
}

// TestSelfHostAnnotateIndexMonoIR_X86_64 pins ExprIndex.ty surviving the
// monomorphisation rebuilds, through the self-host x86-64 IR path. Uses
// asm_load_run.fern — the driver that runs checker.annotate_module — since a
// driver that skips annotation leaves every ty empty and cannot regress.
func TestSelfHostAnnotateIndexMonoIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateIndexMonoCases {
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
			progBin := buildBin(t, gcc, dir, "annidxmono_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
