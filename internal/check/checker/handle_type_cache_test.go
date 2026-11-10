package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestHandleTypeQueriesRetainGenericAndRecursiveHandles(t *testing.T) {
	prog, err := parser.Parse(`
struct Handle { fd: i32 }
function (own h: Handle) close(): void {}
struct Box[T] { value: T }
struct A { children: B[], handle: Handle }
struct B { parents: A[] }
struct PlainA { children: PlainB[] }
struct PlainB { parents: PlainA[] }
enum Link { Next(Link), Held(Handle), End }
enum PlainLink { Next(PlainLink), End }
enum Wrapped[T] { Item(T), Empty }
function main(): i32 { return 0; }
`)
	if err != nil {
		t.Fatal(err)
	}
	handle := ast.StructType{Name: "Handle"}
	cases := []struct {
		typeOf ast.Type
		free   bool
	}{
		{ast.StructType{Name: "PlainA"}, true},
		{ast.StructType{Name: "A"}, false},
		{ast.StructType{Name: "B"}, false},
		{ast.StructType{Name: "PlainB"}, true},
		{ast.StructType{Name: "Box", Args: []ast.Type{ast.StringType{}}}, true},
		{ast.StructType{Name: "Box", Args: []ast.Type{handle}}, false},
		{ast.StructType{Name: "Box", Args: []ast.Type{ast.ParamType{Name: "T"}}}, false},
		{ast.EnumType{Name: "PlainLink"}, true},
		{ast.EnumType{Name: "Link"}, false},
		{ast.EnumType{Name: "Wrapped", Args: []ast.Type{ast.StringType{}}}, true},
		{ast.EnumType{Name: "Wrapped", Args: []ast.Type{handle}}, false},
		{ast.EnumType{Name: "Option", Args: []ast.Type{handle}}, false},
		{ast.ArrayType{Elem: handle}, false},
		{ast.SliceType{Elem: ast.StructType{Name: "PlainA"}}, true},
		{ast.TupleType{Elems: []ast.Type{ast.StringType{}, handle}}, false},
	}
	// A second check sees already hoisted consuming methods.
	for pass := 0; pass < 2; pass++ {
		info, err := Check(prog)
		if err != nil {
			t.Fatal(err)
		}
		for repeat := 0; repeat < 3; repeat++ {
			for _, tc := range cases {
				if got := info.TypeHoldsNoHandle(tc.typeOf); got != tc.free {
					t.Errorf("pass %d query %d %s: got %v, want %v", pass, repeat, tc.typeOf, got, tc.free)
				}
			}
		}
	}
}
