package e2e

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

var updateDiagGolden = flag.Bool("update-diag-golden", false, "regenerate diagnostic golden files")

var diagnosticGoldenCases = []struct {
	name string
	src  string
}{
	{"E001_undefined_suggestion", "function main(): i32 { let count = 1; return kount; }\n"},
	{"E002_return_type", "function f(): i32 { return \"x\"; }\nfunction main(): i32 { return 0; }\n"},
	{"E004_arg_count", "function g(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return g(1); }\n"},
	{"E005_missing_field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p.x; }\n"},
	{"E006_redeclared", "function dup(): i32 { return 1; }\nfunction dup(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n"},
	{"E008_nonbool_if", "function main(): i32 { if (3) { return 1; } return 0; }\n"},
	{"E021_bound_conformance", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { let p: Foo = Foo { x: 1 }; let r: Foo = pick(p); return r.x; }\n"},
	{"E052_missing_return", "function f(): i32 { let x = 1; }\nfunction main(): i32 { return 0; }\n"},
	{"P001_parse_error", "function main(): i32 { return 1 +; }\n"},
	{"E003_assign_mismatch", "function main(): i32 { let x: i32 = \"s\"; return x; }\n"},
	{"E011_break_outside_loop", "function main(): i32 { break; return 0; }\n"},
	{"E013_dup_var", "function main(): i32 { let x = 1; let x = 2; return x; }\n"},
	{"E019_generic_arity", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	{"E020_empty_array_annot", "function main(): i32 { let a = []; return 0; }\n"},
	{"E040_typearg_arity", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32, i32](1); }\n"},
	{"E040_struct_inference", "struct Stack[T] { items: T[] }\nfunction main(): i32 { let s = Stack { items: [] }; return 0; }\n"},
	{"E043_unknown_field", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; return p.y; }\n"},
	{"E048_immutable_field", "struct P { x: i32 }\nfunction main(): i32 { let p = P { x: 1 }; p.x = 2; return p.x; }\n"},
	{"E063_slice_escape", "function f(): [i32] { let xs: i32[] = [1, 2, 3]; return xs[0:2]; }\nfunction main(): i32 { return 0; }\n"},
	{"E064_unknown_type", "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
	{"E024_tuple_destructure", "function main(): i32 { let (a, b) = 5; return a; }\n"},
	{"E026_wildcard_not_last", "function main(): i32 { let x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n"},
	{"E033_invalid_cast", "function main(): i32 { let b: boolean = true; return b as i32; }\n"},
	{"E037_slice_bound", "function main(): i32 { let a = [1, 2, 3]; let s = a[\"x\":2]; return 0; }\n"},
	{"E041_eq_mismatch", "function main(): i32 { if (\"a\" == 1) { return 1; } return 0; }\n"},
	{"E046_tuple_index_oor", "function main(): i32 { let t = (1, 2); return t.5; }\n"},
	{"E047_int_overflow", "function main(): i32 { return 9999999999; }\n"},
	{"E047_const_overflow", "const LIMIT: i32 = 2147483648;\nfunction main(): i32 { return LIMIT; }\n"},
	{"E047_past_u64", "function main(): i32 { let a: u64 = 18446744073709551616; return 0; }\n"},
	{"E055_discarded_result", "function main(): i32 { let a: i32[] = [1]; a.append(2); return 0; }\n"},
	{"E058_labeled_break", "function main(): i32 { let c = 0; while (c < 3) { c = c + 1; if (c == 2) { break nope; } } return c; }\n"},
	{"E061_value_block_no_tail", "function main(): i32 { let x = if (1 < 2) { print(\"hi\"); } else { 2 }; return 0; }\n"},
}

func TestDiagnosticGolden(t *testing.T) {
	goldenDir := filepath.Join("testdata", "diag_golden")
	if *updateDiagGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
	}
	for _, tc := range diagnosticGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderDiagnostic(t, tc.name, tc.src)
			if got == "" {
				t.Fatalf("%s produced no diagnostic — the case no longer exercises its error shape", tc.name)
			}
			goldenPath := filepath.Join(goldenDir, tc.name+".golden")
			if *updateDiagGolden {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update-diag-golden to create)", goldenPath, err)
			}
			if got != string(want) {
				t.Errorf("%s diagnostic drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, string(want))
			}
		})
	}
}

func renderDiagnostic(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	display := name + ".fern"
	prog, _, loadErr := modload.Load(path)
	if loadErr != nil {
		return diag.Format(display, src, loadErr)
	}
	if foldErr := constfold.Fold(prog, nil); foldErr != nil {
		return diag.Format(display, src, foldErr)
	}
	if _, chkErr := checker.Check(prog); chkErr != nil {
		return diag.Format(display, src, chkErr)
	}
	return ""
}
