package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateEnumCases exercise the nominal-ENUM half of the typed-IR carrier
// (#5531, docs/TYPED-IR-REWRITE.md): an enum-valued expression must carry its
// enum's name, so a method call on it dispatches to that enum's method instead
// of failing to resolve the receiver. Each case names the layer it pins:
//
//   - the CARRIER: type_to_irtag names a bare nominal union, so an indexed
//     enum array, a slice, a struct field, a tuple element or a fn-value call
//     stamps the enum rather than "". A `dyn Trait` struct field resolves
//     through the same rule.
//   - the CHECKER: a qualified UNIT variant `Color.Red` in value position types
//     as its enum, by the same rule as the qualified constructor form
//     (qual_variant_union).
//
// Each case ASSERTS "ir" via -decide, so a change that pushes one off the IR
// path fails loudly rather than silently stopping exercising the carrier.
// Oracle: the interp. Every case answers 42.
var annotateEnumCases = []struct {
	name string
	src  string
}{
	// CARRIER: an enum-ARRAY-returning call, indexed.
	{"enum_array_ret_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function mk(): Color[] { return [Color.Green]; }
function main(): i32 { return mk()[0].rank(); }`},
	// CARRIER: a SLICED base, whose element type only the stamp names — the
	// enum twin of the struct case #5986 wired.
	{"enum_slice_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let cs: Color[] = [Color.Red, Color.Green];
    return cs[1:2][0].rank();
}`},
	// CARRIER: a struct FIELD declared at an enum type.
	{"enum_struct_field_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
struct R { c: Color }
function main(): i32 { let r: R = R { c: Color.Green }; return r.c.rank(); }`},
	// CARRIER: the TUPLE-element sibling of the case above, one token apart and
	// through the same arm — the pairing docs/TYPED-IR-REWRITE.md records as the
	// one that gets fixed singly.
	{"enum_tuple_elem_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 { let t: (Color, i32) = (Color.Green, 1); return t.0.rank(); }`},
	// CARRIER: `mkf()()` — the callee is a CALL, so nothing but the stamp on the
	// enclosing call names the result's type.
	{"enum_fn_value_call_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function get(): Color { return Color.Green; }
function mkf(): () => Color { return get; }
function main(): i32 { return mkf()().rank(); }`},
	// CHECKER: a qualified unit variant in an array literal. The whole
	// expression typed unknown before the value-position rule existed, so no
	// stamp downstream of it could be non-empty.
	{"qualified_unit_variant_array_index", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 { return [Color.Red, Color.Green][1].rank(); }`},
	// CHECKER + CARRIER: an enum-array if-EXPRESSION, indexed. The stamp is the
	// only answer for its type, and the qualified variants inside it have to
	// type first.
	{"enum_iife_array_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let b: boolean = true;
    return (if (b) { [Color.Green] } else { [Color.Red] })[0].rank();
}`},

	// --- controls: shapes that resolve without the enum rules must not move ---

	// The STRUCT sibling of the first two cases.
	{"struct_array_ret_index_method", `struct P { v: i32 }
function (p: P) rank(): i32 { return p.v; }
function mk(): P[] { return [P { v: 42 }]; }
function main(): i32 { return mk()[0].rank(); }`},
	// A SCALAR-returning call: a scalar tag must not read as a nominal enum and
	// dispatch `u64.rank`-style on a value that is not one.
	{"scalar_ret_call_method", `function w(): u64 { return 4294967338u64; }
function main(): i32 { return (w() % 100u64) as i32; }`},
	// A `dyn Trait` struct FIELD, whose method call resolves by the same rule as
	// the enum field above.
	{"dyn_struct_field_method", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Holder { sh: dyn Shape }
function main(): i32 { let h: Holder = Holder { sh: Sq { s: 6 } }; return h.sh.area() + 6; }`},
	// A struct field declared at a SCALAR type.
	{"scalar_struct_field_method", `struct B { v: f64 }
function main(): i32 { let b: B = B { v: 4.2 }; return (b.v * 10.0) as i32; }`},
	// Two enums declaring the SAME variant name, in value position. The first
	// draft of qual_variant_union resolved the owner with union_of_variant — a
	// first-match scan over every union — which typed `B.Zed` as A and keyed the
	// lowering on the wrong enum. The written enum is the answer, and there is
	// nothing to recover by scanning: qual_enum_name only answers for a name
	// lookup_union matched exactly. `Zed` sits at a different ordinal in each
	// enum, so a cross-enum resolution picks a real-but-wrong slot rather than
	// failing to find one. Conformance's `shared_variant_name` is the sibling
	// that caught it; this is the pin next to the rule that can regress it.
	{"shared_variant_name_value", `enum A { Zed, Xx(i32) }
enum B { Yy(i32), Zed }
function (a: A) rank(): i32 {
    match (a) { A.Zed => { return 40; }, A.Xx(n) => { return n; } }
    return 0;
}
function (b: B) rank(): i32 {
    match (b) { B.Zed => { return 42; }, B.Yy(n) => { return n; } }
    return 0;
}
function main(): i32 { let v: B = B.Zed; return v.rank(); }`},
	// Bare (unqualified) variant spellings, the form that always typed: the
	// value-position rule must not change what they resolve to.
	{"bare_variant_payload_method", `enum Color { Red, Green(i32), Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green(v) => { return v; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    let b: boolean = true;
    return (if (b) { [Green(42)] } else { [Red] })[0].rank();
}`},
}

// TestSelfHostAnnotateEnumIR_X86_64 pins the nominal-enum carrier through the
// self-host x86-64 IR path. asm_load_run.fern is the driver because it runs
// checker.annotate_module after the checker gate and before emit.
func TestSelfHostAnnotateEnumIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateEnumCases {
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
			progBin := buildBin(t, gcc, dir, "annenum_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostAnnotateEnumWasm is the wasm leg. Both backends bailed identically
// on the seven gap cases, so this leg carries no distinct defect — it is what
// proves the recovered receiver type is emitted as a module the validator
// accepts, which x86-64 does not check.
func TestSelfHostAnnotateEnumWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-enum wasm cases")
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

	for _, tc := range annotateEnumCases {
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
