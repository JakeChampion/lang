package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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

func TestPatternForEachChoosesLoopByIterandType(t *testing.T) {
	arrayProg := `function f(): i32 {
		let xs: (i32, i32)[] = [(1, 2)];
		let sum: i32 = 0;
		for (a, b) in xs { sum = sum + a + b; }
		return sum;
	}`
	mapProg := `function f(): i32 {
		let m: Map[i32, i32] = map_new(4);
		let sum: i32 = 0;
		for (k, v) in m { sum = sum + k + v; }
		return sum;
	}`

	arrBlk := checkedForEachBlock(t, arrayProg)
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
	if len(mapBlk.Stmts) != 2 {
		t.Fatalf("map lowering: expected 2 stmts (cursor/for), got %d", len(mapBlk.Stmts))
	}
	cursor, ok := mapBlk.Stmts[0].(*ast.Var)
	if !ok {
		t.Fatalf("map lowering: first stmt should bind the cursor, got %T", mapBlk.Stmts[0])
	}
	call, ok := cursor.Init.(*ast.Call)
	if !ok {
		t.Fatalf("map lowering: the cursor is `m.iter()`, got %T", cursor.Init)
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || callee.Name != "__method_Map_iter" {
		t.Fatalf("map lowering: the cursor is `m.iter()`, got %#v", call.Callee)
	}
	mapLoop, ok := mapBlk.Stmts[1].(*ast.For)
	if !ok {
		t.Fatalf("map lowering: last stmt should be the For, got %T", mapBlk.Stmts[1])
	}
	if mapLoop.Step == nil {
		t.Errorf("map lowering: the For needs a step (advance) so `continue` advances the cursor")
	}
}

func checkedForEachBlock(t *testing.T, src string) *ast.Block {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	blk, ok := prog.Funcs[0].Body.Stmts[2].(*ast.Block)
	if !ok {
		t.Fatalf("the ForEach should have been replaced by its lowering, got %T", prog.Funcs[0].Body.Stmts[2])
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
				let xs: (i32, i32)[] = [(1, 2)];
				for (a, b, c) in xs { return a + b + c; }
				return 0;
			}`,
			want: "tuple has 2 elements, but 3 names given",
		},
		{
			name: "map needs a pair",
			src: `function f(): i32 {
				let m: Map[i32, i32] = map_new(4);
				for (k, v, extra) in m { return k + v + extra; }
				return 0;
			}`,
			want: "iterating a Map binds a (key, value) pair, but this pattern binds 3",
		},
		{
			name: "element is not a tuple",
			src: `function f(): i32 {
				let xs: i32[] = [1, 2];
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
