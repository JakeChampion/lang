package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateFieldCases extend the typed-IR annotation (#5531) from ExprCall to
// ExprFieldAccess (#5986). checker.annotate_expr now stamps a field / tuple-
// element read with its inferred tag, and irlower's fa_type_tag is the single
// leaf every consumer of a field read's type goes through: expr_struct_type,
// expr_map_type_tag, infer_expr_width and the four numeric-kind predicates
// (expr_is_f64 / _f32 / _u32 / _u64). Before this each of those re-derived
// "what type is obj.field?" on its own — infer_expr_width open-coded a second
// obj → struct → field walk via struct_field_is_i64 that was exactly the tag
// query the others made.
//
// Each program routes a field read into one of those consumers, oracle-checked
// against the interpreter. The unsigned cases matter beyond the tag lookup:
// a u64 / u32 field whose type is lost reads signed-negative once its top bit
// is set, so the shift lowers as shr_s and diverges from the oracle.
var annotateFieldCases = []struct {
	name string
	src  string
}{
	// u64 / u32 struct fields feeding an unsigned shift (expr_is_u64/_u32).
	{"unsigned_fields", `struct Bits { hi: u64, lo: u32 }
function main(): i32 {
    var b: Bits = Bits { hi: 18446744073709551615, lo: 4000000000 };
    if ((b.hi >> 1) < 9000000000000000000) { return 1; }
    if ((b.lo >> 1) < 1000000000) { return 2; }
    return 42;
}`},
	// an i64 struct field is a 64-bit value (infer_expr_width).
	{"i64_field_width", `struct Wide { n: i64 }
function main(): i32 {
    var w: Wide = Wide { n: 5000000000 };
    var v: i64 = w.n + 1;
    if (v == 5000000001) { return 42; }
    return 7;
}`},
	// f64 struct fields in float arithmetic (expr_is_f64).
	{"f64_fields", `struct Pt { x: f64, y: f64 }
function main(): i32 {
    var p: Pt = Pt { x: 1.5, y: 2.5 };
    var s: f64 = p.x * p.y;
    if (s == 3.75) { return 42; }
    return 7;
}`},
	// a struct-typed field read types the nested access (expr_struct_type).
	{"nested_struct_field", `struct Inner { n: i64 }
struct Outer { inn: Inner }
function main(): i32 {
    var o: Outer = Outer { inn: Inner { n: 5000000000 } };
    if (o.inn.n == 5000000000) { return 42; }
    return 7;
}`},
	// a Map-typed struct field keeps its K/V (expr_map_type_tag).
	{"map_field", `import "core/map";

struct Reg { caps: Map[string, i32] }

function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 40);
    var r: Reg = Reg { caps: m };
    return r.caps.get_or("a", 0) + 2;
}`},
	// a struct TUPLE element, then an unsigned field off it — the tuple half of
	// fa_type_tag feeding the struct half.
	{"tuple_elem_struct_field", `struct P { v: u64 }
function main(): i32 {
    var t: (P, i32) = (P { v: 18446744073709551615 }, 3);
    if ((t.0.v >> 1) > 9000000000000000000) { return 42; }
    return 7;
}`},
}

// TestSelfHostAnnotateFieldIR_X86_64 pins the checker-stamped field-read type
// feeding irlower's fa_type_tag through the self-host x86-64 IR path (#5986).
func TestSelfHostAnnotateFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)
	for _, tc := range annotateFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			// The annotation is consumed on the IR path; assert the module
			// routes there so the case keeps exercising fa.ty.
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
			progBin := buildBin(t, gcc, dir, "annfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
