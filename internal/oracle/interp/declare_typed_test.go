package interp

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// A parameter or `let` whose type cannot reach a Map is bound without
// walking its value, so passing an n-element `i32[]` to a function costs
// no element visits. Counting visits keeps the assertion deterministic:
// growing the array by appends walks it a few times over, while a walk
// per binding would visit every element twice a call, 2·n·calls in all.
func TestTypedBindingSkipsWalk(t *testing.T) {
	const n, calls = 4000, 1000
	src := fmt.Sprintf(`function peek(a: i32[], i: i32): i32 {
		let b: i32[] = a;
		return b[i];
	}
	function main(): i32 {
		let a: i32[] = [];
		let i: i32 = 0;
		while (i < %d) { a = a.append(i); i = i + 1; }
		let t: i32 = 0;
		let j: i32 = 0;
		while (j < %d) { t = t + peek(a, j); a = a.with(j, 0); j = j + 1; }
		return t;
	}`, n, calls)
	before := rcWalked.Load()
	v, ip := evalProgram(t, src)
	walked := rcWalked.Load() - before
	if v != Number(calls*(calls-1)/2) {
		t.Fatalf("got %v, want %d", v, calls*(calls-1)/2)
	}
	if walked > 3*n {
		t.Errorf("walked %d elements, want at most %d", walked, 3*n)
	}
	if ip.arraySetCopies != 0 {
		t.Errorf("copied %d elements, want 0", ip.arraySetCopies)
	}
}

func TestHoldsNoMap(t *testing.T) {
	mapT := ast.StructType{Name: "Map", Args: []ast.Type{ast.StringType{}, ast.NumberType{Width: 32, Signed: true}}}
	cases := []struct {
		name string
		t    ast.Type
		want bool
	}{
		{"i32", ast.NumberType{Width: 32, Signed: true}, true},
		{"u8[][]", ast.ArrayType{Elem: ast.ArrayType{Elem: ast.NumberType{Width: 8}}}, true},
		{"(f64, string[], boolean)", ast.TupleType{Elems: []ast.Type{ast.FloatType{Width: 64}, ast.ArrayType{Elem: ast.StringType{}}, ast.BoolType{}}}, true},
		{"Map", mapT, false},
		{"Map[]", ast.ArrayType{Elem: mapT}, false},
		{"(i32, Map)", ast.TupleType{Elems: []ast.Type{ast.NumberType{Width: 32}, mapT}}, false},
		{"a struct", ast.StructType{Name: "Point"}, false},
		{"a type parameter", ast.ParamType{Name: "T"}, false},
		{"unknown", nil, false},
	}
	for _, c := range cases {
		if got := holdsNoMap(c.t); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A scalar has no walk to skip, so binding one allocates no bookkeeping:
// every call with an i32 parameter would otherwise pay for a map.
func TestTypedScalarBindingAllocatesNothing(t *testing.T) {
	e := newEnv(nil)
	e.declareTyped("n", Number(1), ast.NumberType{Width: 32, Signed: true})
	if e.unwalked != nil {
		t.Errorf("binding an i32 allocated unwalked: %v", e.unwalked)
	}
	e.declareTyped("a", newArray(2), ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}})
	if !e.unwalked["a"] {
		t.Errorf("binding an i32[] did not skip its walk")
	}
}

// An enum whose payloads reach no Map or Array is not walked: consing an
// n-node list of scalars reassigns the list n times, and a walk per store
// would visit every node so far, n²/2 payloads in all.
func TestScalarEnumListSkipsWalk(t *testing.T) {
	const n = 4000
	src := fmt.Sprintf(`enum L { Nil, Cons(i32, L) }
	function main(): i32 {
		let l: L = Nil;
		let i: i32 = 0;
		while (i < %d) { l = Cons(i, l); i = i + 1; }
		match (l) { Cons(h, t) => { return h; }, Nil => { return 0 - 1; } }
	}`, n)
	before := rcWalked.Load()
	v, _ := evalProgram(t, src)
	walked := rcWalked.Load() - before
	if v != Number(n-1) {
		t.Fatalf("got %v, want %d", v, n-1)
	}
	if walked > n {
		t.Errorf("walked %d payloads, want at most %d", walked, n)
	}
}

// An enum carrying a Map is still walked, so the map's count sees the
// enum's slot and a write through another alias copies it.
func TestEnumCarryingMapIsCounted(t *testing.T) {
	src := `import "core/map";
	enum Box { Full(Map[string, i32]), Empty }
	function main(): i32 {
		let m: Map[string, i32] = map_new(4);
		m = m.insert("a", 1);
		let b: Box = Full(m);
		m = m.insert("a", 2);
		match (b) { Full(inner) => { return inner.get_or("a", 0) * 10 + m.get_or("a", 0); }, Empty => { return 0; } }
	}`
	v, _ := evalProgram(t, src)
	if v != Number(12) {
		t.Fatalf("got %v, want 12: the enum's map changed when the alias was written", v)
	}
}
