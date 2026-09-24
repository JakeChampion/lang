package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// checkedInfo type-checks src and hands back the checker Info the drop-safety
// walkers consult. Deliberately no monomorph pass: a generic enum whose
// payloads are bare type params is left generic all the way to the IR
// (monomorph.enumNeedsClone), which is the shape under test.
func checkedInfo(t *testing.T, src string) *checker.Info {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return info
}

// A user-declared generic enum is judged by the same rule as the auto-injected
// Option / Result: bind the instantiation's arguments to the enum's type
// parameters, then ask the payloads. Before this, the payload `T` was read as a
// bare ParamType — which no case of the walk accepts — so every user generic
// enum fell out of the deep-drop set while `Option[T]` had a by-name bypass.
func TestSelfDropSafeBindsGenericEnumArgs(t *testing.T) {
	info := checkedInfo(t, `enum Maybe[T] { Just(T), Nothing }
enum Pair[A, B] { Both(A, B), Neither }
struct Box { v: i32 }
function main(): i32 { return 0; }`)

	mapType := ast.StructType{Name: "Map", Args: []ast.Type{ast.StringType{}, ast.NumberType{}}}
	cases := []struct {
		name string
		ty   ast.Type
		want bool
	}{
		{"Option[string]", ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}}, true},
		{"Maybe[string]", ast.EnumType{Name: "Maybe", Args: []ast.Type{ast.StringType{}}}, true},
		{"Maybe[Box]", ast.EnumType{Name: "Maybe", Args: []ast.Type{ast.StructType{Name: "Box"}}}, true},
		{"Maybe[string[]]", ast.EnumType{Name: "Maybe", Args: []ast.Type{ast.ArrayType{Elem: ast.StringType{}}}}, true},
		{"Maybe[Option[string]]", ast.EnumType{Name: "Maybe", Args: []ast.Type{ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}}}}, true},
		// Map has an incomplete deep drop, and the binding has to carry that
		// through the payload rather than stopping at the enum's own name.
		{"Maybe[Map]", ast.EnumType{Name: "Maybe", Args: []ast.Type{mapType}}, false},
		{"Option[Map]", ast.EnumType{Name: "Option", Args: []ast.Type{mapType}}, false},
		{"Pair[string, Map]", ast.EnumType{Name: "Pair", Args: []ast.Type{ast.StringType{}, mapType}}, false},
		{"Pair[string, i32]", ast.EnumType{Name: "Pair", Args: []ast.Type{ast.StringType{}, ast.NumberType{}}}, true},
		// An undeclared enum name still has no payloads to consult.
		{"Ghost[i32]", ast.EnumType{Name: "Ghost", Args: []ast.Type{ast.NumberType{}}}, false},
	}
	for _, tc := range cases {
		if got := typeSelfDropSafe(tc.ty, info, map[string]bool{}); got != tc.want {
			t.Errorf("typeSelfDropSafe(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The recursion guard keys on the INSTANTIATED type, not the bare enum name:
// two instantiations of one generic enum in the same walk answer the question
// separately. Keyed by name, the first sibling's entry would short-circuit the
// second to "safe" and hand a Map-carrying struct a deep drop it cannot
// complete.
func TestSelfDropSafeSiblingInstantiationsAreDistinct(t *testing.T) {
	info := checkedInfo(t, `enum Maybe[T] { Just(T), Nothing }
struct S { a: Maybe[string], b: Maybe[Map[string, i32]] }
struct T2 { a: Maybe[string], b: Maybe[i32] }
function main(): i32 { return 0; }`)

	if typeSelfDropSafe(ast.StructType{Name: "S"}, info, map[string]bool{}) {
		t.Error("struct S read as deep-droppable: its Maybe[Map[…]] field was short-circuited by the Maybe[string] sibling")
	}
	if !typeSelfDropSafe(ast.StructType{Name: "T2"}, info, map[string]bool{}) {
		t.Error("struct T2 read as not deep-droppable: both Maybe instantiations carry droppable payloads")
	}
}

// A self-referential enum still terminates: the guard fires on the repeat of
// the same instantiated type.
func TestSelfDropSafeRecursiveEnumTerminates(t *testing.T) {
	info := checkedInfo(t, `enum Tree { Node(Tree[]), Leaf }
function main(): i32 { return 0; }`)

	if !typeSelfDropSafe(ast.EnumType{Name: "Tree"}, info, map[string]bool{}) {
		t.Error("Tree read as not deep-droppable: every payload it carries is droppable")
	}
}
