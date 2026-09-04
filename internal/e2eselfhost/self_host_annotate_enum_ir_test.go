package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateEnumCases exercise the nominal-ENUM half of the typed-IR carrier
// (#5531, docs/TYPED-IR-REWRITE.md). The checker's tag namer had a
// `TypeStruct(st) => st.name` arm and no union sibling, so every enum-valued
// expression stamped "" — while irlower's admission helper struct_tag_from_ty
// had admitted an enum name all along. Seven shapes below therefore could not
// resolve their receiver's type, took expr_recv_prim_type's "i32" default, and
// bailed the module on the unknown symbol `i32.rank`; the interpreter, the
// semantic oracle, runs all of them.
//
// Three layers were responsible and each case names which one it pins:
//
//   - the CARRIER: type_to_irtag now names a bare nominal union, and
//     expr_struct_type's ExprCall / ExprFieldAccess arms admit it through
//     struct_tag_from_ty instead of decl_is_struct (which answers only half the
//     question its callers ask). That guard also discarded a `dyn Trait` field
//     spelling the call arm had always kept, so a dyn-field case pins it too.
//   - the WALK: struct_ret_fns_of's ARRAY arms had no enum element sibling, so
//     an `E[]`-returning function recorded nothing. That is the answer the
//     UNANNOTATED drivers (asm_ir_run, the native compiler) still depend on.
//   - the CHECKER: a qualified UNIT variant `Color.Red` in value position typed
//     unknown — check_call_expr learned the qualified constructor form in #6657
//     and check_expr's field-access arm never learned the value sibling — so a
//     carrier alone would have stamped "" on exactly the expressions that needed
//     it. Both spellings ask one rule now (qual_variant_union).
//
// Each case ASSERTS "ir" via -decide, so a change that pushes one off the IR
// path fails loudly rather than silently stopping exercising the carrier.
// Oracle: the interp. Every case answers 42.
//
// One consequence of the widening is NOT pinned here and cannot be: routing two
// more arms through struct_tag_from_ty required tightening its enum admission
// from the loose is_enum_like_name to decl_is_enum, because an ERASED generic
// struct name (`OrdSet`, stamped before monomorphisation, with only the concrete
// clones left to declare it) satisfies the loose test. That needs a generic
// MODULE to reproduce, so TestSelfHostStdTestE2E — which compiles the stdlib —
// is the gate that holds it, and it is the gate that caught it.
var annotateEnumCases = []struct {
	name string
	src  string
}{
	// CARRIER + WALK: an enum-ARRAY-returning call, indexed. struct_ret_fns_of
	// recorded the element type for a struct return and not for an enum one.
	{"enum_array_ret_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function mk(): Color[] { return [Color.Green]; }
function main(): i32 { return mk()[0].rank(); }`},
	// CARRIER: a SLICED base reaches no walk arm at all, so the element type can
	// only come from the stamp — the enum twin of the struct case #5986 wired.
	{"enum_slice_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    var cs: Color[] = [Color.Red, Color.Green];
    return cs[1:2][0].rank();
}`},
	// CARRIER: a struct FIELD declared at an enum type. fa_type_tag resolved the
	// spelling and expr_struct_type discarded it, because decl_is_struct is not
	// the question the arm's callers ask.
	{"enum_struct_field_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
struct R { c: Color }
function main(): i32 { var r: R = R { c: Color.Green }; return r.c.rank(); }`},
	// CARRIER: the TUPLE-element sibling of the case above, one token apart and
	// through the same arm — the pairing docs/TYPED-IR-REWRITE.md records as the
	// one that gets fixed singly.
	{"enum_tuple_elem_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 { var t: (Color, i32) = (Color.Green, 1); return t.0.rank(); }`},
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
	// CHECKER + CARRIER: an enum-array if-EXPRESSION, indexed. An enum-valued
	// if-expression is NOT lifted to a `__lam_` (its variant constructors read as
	// captures), so struct_ret_fns_of's lifted-leaf arm cannot see it and the
	// stamp is the only answer — which the qualified variants inside it had to
	// type first.
	{"enum_iife_array_index_method", `enum Color { Red, Green, Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green => { return 42; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    var b: boolean = true;
    return (if (b) { [Color.Green] } else { [Color.Red] })[0].rank();
}`},

	// --- controls: the walk paths the tag now sits beside must not move ---

	// The STRUCT sibling of the first two cases, which worked before this slice
	// and must keep working: the tag must not displace struct_ret_fns_of's own
	// element answer.
	{"struct_array_ret_index_method", `struct P { v: i32 }
function (p: P) rank(): i32 { return p.v; }
function mk(): P[] { return [P { v: 42 }]; }
function main(): i32 { return mk()[0].rank(); }`},
	// A SCALAR-returning call reached through the widened admission helper. If
	// struct_tag_from_ty stopped rejecting the scalar tag vocabulary first, an
	// `i64` tag would read as a nominal enum and dispatch `i64.rank`-style on a
	// value that is not one — the exact failure is_enum_like_name's exclusion
	// list exists to prevent.
	{"scalar_ret_call_method", `function w(): u64 { return 4294967338u64; }
function main(): i32 { return (w() % 100u64) as i32; }`},
	// A `dyn Trait` struct FIELD, which was a live bail for the same reason as
	// the enum cases and is closed by the same one-rule change:
	// expr_struct_type's ExprCall arm has returned the coarse "dyn Trait" type
	// from struct_ret_fns_of for a long time, and the FIELD arm's decl_is_struct
	// guard threw the identical spelling away. Measured at 42 against the interp
	// oracle where the pre-change compiler refused the module outright.
	{"dyn_struct_field_method", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Holder { sh: dyn Shape }
function main(): i32 { var h: Holder = Holder { sh: Sq { s: 6 } }; return h.sh.area() + 6; }`},
	// A struct field declared at a SCALAR type, through the arm whose guard
	// changed from decl_is_struct to struct_tag_from_ty.
	{"scalar_struct_field_method", `struct B { v: f64 }
function main(): i32 { var b: B = B { v: 4.2 }; return (b.v * 10.0) as i32; }`},
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
function main(): i32 { var v: B = B.Zed; return v.rank(); }`},
	// Bare (unqualified) variant spellings, the form that always typed: the
	// value-position rule must not change what they resolve to.
	{"bare_variant_payload_method", `enum Color { Red, Green(i32), Blue }
function (c: Color) rank(): i32 {
    match (c) { Color.Red => { return 1; }, Color.Green(v) => { return v; }, _ => { return 3; } }
    return 0;
}
function main(): i32 {
    var b: boolean = true;
    return (if (b) { [Green(42)] } else { [Red] })[0].rank();
}`},
}

// TestSelfHostAnnotateEnumIR_X86_64 pins the nominal-enum carrier through the
// self-host x86-64 IR path. asm_load_run.fern is the driver because it runs
// checker.annotate_module after the checker gate and before emit; asm_ir_run and
// the native compiler skip the pass, so under those the WALK cases above answer
// from struct_ret_fns_of alone.
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
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
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
