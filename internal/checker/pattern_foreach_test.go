package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// checkSrc parses and checks a program, returning the diagnostics as one string.
func checkSrc(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		return err.Error()
	}
	return ""
}

// A destructuring `for (a, b) in …` header reaches the checker un-lowered; the
// loop it becomes is chosen HERE, by the iterand's type. An array binds the
// pattern against each element (index loop); a Map binds it against each entry
// (cursor loop).
func TestPatternForEachChoosesLoopByIterandType(t *testing.T) {
	arrayProg := `function f(): i32 {
		var xs: (i32, i32)[] = [(1, 2)];
		var sum: i32 = 0;
		for (a, b) in xs { sum = sum + a + b; }
		return sum;
	}`
	mapProg := `function f(): i32 {
		var m: Map[i32, i32] = map_new(4);
		var sum: i32 = 0;
		for (k, v) in m { sum = sum + k + v; }
		return sum;
	}`

	arrBlk := checkedForEachBlock(t, arrayProg)
	// iter / len / idx / for — the same shape the plain `for x in xs` form
	// lowers to, with the pattern bound inside the loop body.
	if len(arrBlk.Stmts) != 4 {
		t.Fatalf("array lowering: expected 4 stmts (iter/len/idx/for), got %d", len(arrBlk.Stmts))
	}
	loop, ok := arrBlk.Stmts[3].(*ast.For)
	if !ok {
		t.Fatalf("array lowering: last stmt should be the For, got %T", arrBlk.Stmts[3])
	}
	inner, ok := loop.Body.(*ast.Block)
	if !ok || len(inner.Stmts) < 2 {
		t.Fatalf("array lowering: loop body should bind the element then destructure it, got %T", loop.Body)
	}
	if _, ok := inner.Stmts[1].(*ast.Destructure); !ok {
		t.Fatalf("array lowering: the pattern must bind through the shared *ast.Destructure, got %T", inner.Stmts[1])
	}

	mapBlk := checkedForEachBlock(t, mapProg)
	// iterand / cursor / for — the entry cursor keeps the Map walk
	// allocation-free, so it must NOT lower to the index loop.
	if len(mapBlk.Stmts) != 3 {
		t.Fatalf("map lowering: expected 3 stmts (iterand/cursor/for), got %d", len(mapBlk.Stmts))
	}
	cursor, ok := mapBlk.Stmts[1].(*ast.Var)
	if !ok {
		t.Fatalf("map lowering: second stmt should bind the cursor, got %T", mapBlk.Stmts[1])
	}
	// Checked, so the method call is already rewritten to its mangled name.
	call, ok := cursor.Init.(*ast.Call)
	if !ok {
		t.Fatalf("map lowering: the cursor is `m.iter()`, got %T", cursor.Init)
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Map_iter" {
		t.Fatalf("map lowering: the cursor is `m.iter()`, got %#v", call.Callee)
	}
	mapLoop, ok := mapBlk.Stmts[2].(*ast.For)
	if !ok {
		t.Fatalf("map lowering: last stmt should be the For, got %T", mapBlk.Stmts[2])
	}
	if mapLoop.Step == nil {
		t.Errorf("map lowering: the For needs a step (advance) so `continue` advances the cursor")
	}
}

// checkedForEachBlock checks a one-loop program and returns the Block the
// pattern foreach lowered to.
func checkedForEachBlock(t *testing.T, src string) *ast.Block {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	wrapper, ok := prog.Funcs[0].Body.Stmts[2].(*ast.Block)
	if !ok {
		t.Fatalf("expected the parser's wrapper Block, got %T", prog.Funcs[0].Body.Stmts[2])
	}
	blk, ok := wrapper.Stmts[0].(*ast.Block)
	if !ok {
		t.Fatalf("the ForEach should have been replaced by its lowering, got %T", wrapper.Stmts[0])
	}
	return blk
}

func TestPatternForEachArityDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "array element arity",
			src: `function f(): i32 {
				var xs: (i32, i32)[] = [(1, 2)];
				for (a, b, c) in xs { return a + b + c; }
				return 0;
			}`,
			want: "tuple has 2 elements, but 3 names given",
		},
		{
			name: "map needs a pair",
			src: `function f(): i32 {
				var m: Map[i32, i32] = map_new(4);
				for (k, v, extra) in m { return k + v + extra; }
				return 0;
			}`,
			want: "iterating a Map binds a (key, value) pair, but this pattern binds 3",
		},
		{
			name: "element is not a tuple",
			src: `function f(): i32 {
				var xs: i32[] = [1, 2];
				for (a, b) in xs { return a + b; }
				return 0;
			}`,
			want: "tuple destructure needs a tuple expression, got i32",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("diagnostics = %q, want one containing %q", got, tc.want)
			}
		})
	}
}
