package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func TestResolvedArrayAppendIntrinsic(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		elem         ast.Type
	}{
		{"string", `function pilot(items: string[], item: string): string[] { return items.append(item); }`, ast.StringType{}},
		{"i64-literal", `function pilot(items: i64[]): i64[] { return items.append(7); }`, ast.NumberType{Width: 64, Signed: true}},
		{"nested-array", `function pilot(items: string[][], item: string[]): string[][] { return items.append(item); }`, ast.ArrayType{Elem: ast.StringType{}}},
		{"typed-empty", `function pilot(item: string): string[] { var items: string[] = []; return items.append(item); }`, ast.StringType{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.source + "\nfunction main(): i32 { return 0; }")
			if err != nil {
				t.Fatal(err)
			}
			// Rechecking a rewritten call must resolve the same identity and
			// concrete type, not depend on the original method-call syntax.
			for round := 0; round < 2; round++ {
				info, err := Check(prog)
				if err != nil {
					t.Fatal(err)
				}
				if len(info.IntrinsicCalls) != 1 {
					t.Fatalf("round %d: got %d intrinsic calls", round, len(info.IntrinsicCalls))
				}
				for call, resolved := range info.IntrinsicCalls {
					sig := resolved.Signature
					if resolved.Kind != IntrinsicArrayAppend || sig == nil || len(sig.Params) != 2 || len(call.Args) != 2 {
						t.Fatalf("invalid intrinsic contract: %+v", resolved)
					}
					array := ast.ArrayType{Elem: tc.elem}
					if !ast.Equal(sig.Params[0], array) || !ast.Equal(sig.Params[1], tc.elem) || !ast.Equal(sig.Result, array) {
						t.Fatalf("lost instantiated signature: %+v", sig)
					}
				}
			}
		})
	}
}

func TestUserAppendNameDoesNotGrantIntrinsicIdentity(t *testing.T) {
	prog, err := parser.Parse(`
function append(items: string[], item: string): string[] { return items; }
function pilot(items: string[], item: string): string[] { return append(items, item); }
function main(): i32 { return 0; }
`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.IntrinsicCalls) != 0 {
		t.Fatal("a user declaration's name/signature does not make it an intrinsic")
	}
}
