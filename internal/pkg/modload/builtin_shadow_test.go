package modload_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

// The entry's declaration of a built-in type's name takes a bundle name of its
// own, so an imported module's reference to the name still means the built-in.
func TestEntryDeclarationOfBuiltinNameIsRenamed(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/string";
enum Span { Empty, Wide(f64) }
function (s: Span) wide(): Span { return Span.Wide(1.0); }
function f(s: Span): i32 { match (s) { Span.Empty => { return 0; }, _ => { return 1; } } }
function main(): i32 { return f(Span.Empty); }
`)
	if err != nil {
		t.Fatal(err)
	}
	bundle := ast.BuiltinShadowName("Span")
	if got := prog.BuiltinShadows[bundle]; got != "Span" {
		t.Fatalf("BuiltinShadows[%q] = %q, want Span", bundle, got)
	}
	var entryEnum bool
	for _, ed := range prog.Enums {
		if ed.Name == "Span" {
			t.Errorf("an enum still carries the built-in's name Span")
		}
		entryEnum = entryEnum || ed.Name == bundle
	}
	if !entryEnum {
		t.Fatalf("no enum named %s in the bundle", bundle)
	}
	for _, fn := range prog.Funcs {
		switch fn.Name {
		case "wide":
			if fn.Receiver == nil || fn.Receiver.Type.String() != bundle || fn.ReturnType.String() != bundle {
				t.Errorf("method receiver/return = %v/%v, want %s", fn.Receiver, fn.ReturnType, bundle)
			}
		case "f":
			if fn.Params[0].Type.String() != bundle {
				t.Errorf("f's parameter is %v, want %s", fn.Params[0].Type, bundle)
			}
		}
	}
	builtinRefs := 0
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if lit, ok := n.(*ast.StructLit); ok && lit.TypeName == "Span" {
			builtinRefs++
		}
		return true
	})
	if builtinRefs == 0 {
		t.Errorf("no imported module still builds the built-in Span by name")
	}
}
