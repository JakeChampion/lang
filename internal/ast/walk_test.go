package ast

import (
	"reflect"
	"testing"
)

// collectKinds records the dynamic type name of each visited node
// in the order Walk reports them. Useful for asserting traversal
// shape without depending on exact field values.
func collectKinds(root Node) []string {
	var got []string
	Walk(root, func(n Node) bool {
		got = append(got, reflect.TypeOf(n).String())
		return true
	})
	return got
}

func TestWalk_LeafExpr(t *testing.T) {
	root := &NumberLit{P: Position{1, 1}, Value: 7}
	got := collectKinds(root)
	if !reflect.DeepEqual(got, []string{"*ast.NumberLit"}) {
		t.Fatalf("leaf walk: got %v", got)
	}
}

func TestWalk_NilRootIsNoOp(t *testing.T) {
	var calls int
	Walk(nil, func(Node) bool { calls++; return true })
	if calls != 0 {
		t.Fatalf("nil root should not call fn, got %d", calls)
	}
}

func TestWalk_StopDescent(t *testing.T) {
	// Binary(1, 2) — if fn returns false on the Binary node, the
	// two NumberLit children must NOT be visited.
	root := &Binary{
		P:     Position{1, 1},
		Op:    "+",
		Left:  &NumberLit{P: Position{1, 1}, Value: 1},
		Right: &NumberLit{P: Position{1, 5}, Value: 2},
	}
	var visited []string
	Walk(root, func(n Node) bool {
		visited = append(visited, reflect.TypeOf(n).String())
		return false // refuse to descend
	})
	if !reflect.DeepEqual(visited, []string{"*ast.Binary"}) {
		t.Fatalf("stop-descent: got %v", visited)
	}
}

func TestWalk_BinaryDepthFirst(t *testing.T) {
	root := &Binary{
		P:     Position{1, 1},
		Op:    "+",
		Left:  &NumberLit{P: Position{1, 1}, Value: 1},
		Right: &Ident{P: Position{1, 5}, Name: "x"},
	}
	got := collectKinds(root)
	want := []string{"*ast.Binary", "*ast.NumberLit", "*ast.Ident"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("binary walk: got %v want %v", got, want)
	}
}

func TestWalk_CallVisitsCalleeThenArgs(t *testing.T) {
	root := &Call{
		P:      Position{1, 1},
		Callee: &Ident{P: Position{1, 1}, Name: "foo"},
		Args: []Expr{
			&NumberLit{P: Position{1, 5}, Value: 1},
			&NumberLit{P: Position{1, 8}, Value: 2},
		},
	}
	got := collectKinds(root)
	want := []string{"*ast.Call", "*ast.Ident", "*ast.NumberLit", "*ast.NumberLit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call walk: got %v want %v", got, want)
	}
}

func TestWalk_BlockOfStatements(t *testing.T) {
	root := &Block{
		P: Position{1, 1},
		Stmts: []Stmt{
			&Var{P: Position{2, 1}, Name: "x", Init: &NumberLit{P: Position{2, 9}, Value: 1}},
			&Return{P: Position{3, 1}, Value: &Ident{P: Position{3, 8}, Name: "x"}},
		},
	}
	got := collectKinds(root)
	want := []string{
		"*ast.Block",
		"*ast.Var", "*ast.NumberLit",
		"*ast.Return", "*ast.Ident",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("block walk: got %v want %v", got, want)
	}
}

func TestWalk_IfWithElse(t *testing.T) {
	root := &If{
		P:    Position{1, 1},
		Cond: &BoolLit{P: Position{1, 4}, Value: true},
		Then: &Block{P: Position{1, 10}, Stmts: []Stmt{
			&Return{P: Position{2, 1}, Value: &NumberLit{P: Position{2, 8}, Value: 1}},
		}},
		Else: &Block{P: Position{3, 1}, Stmts: []Stmt{
			&Return{P: Position{4, 1}, Value: &NumberLit{P: Position{4, 8}, Value: 2}},
		}},
	}
	got := collectKinds(root)
	want := []string{
		"*ast.If",
		"*ast.BoolLit",
		"*ast.Block", "*ast.Return", "*ast.NumberLit",
		"*ast.Block", "*ast.Return", "*ast.NumberLit",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("if walk: got %v want %v", got, want)
	}
}

func TestWalk_StructLitVisitsFieldValues(t *testing.T) {
	root := &StructLit{
		P:        Position{1, 1},
		TypeName: "Point",
		Fields: []FieldInit{
			{Name: "x", Value: &NumberLit{P: Position{1, 10}, Value: 3}},
			{Name: "y", Value: &Ident{P: Position{1, 16}, Name: "v"}},
		},
	}
	got := collectKinds(root)
	want := []string{"*ast.StructLit", "*ast.NumberLit", "*ast.Ident"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("struct lit walk: got %v want %v", got, want)
	}
}

func TestWalk_MatchVisitsTagThenArmBodies(t *testing.T) {
	root := &Match{
		P:   Position{1, 1},
		Tag: &Ident{P: Position{1, 7}, Name: "e"},
		Arms: []*MatchArm{
			{
				P:           Position{2, 5},
				VariantName: "Some",
				Bindings:    []string{"v"},
				Body: &Block{P: Position{2, 20}, Stmts: []Stmt{
					&Return{P: Position{2, 22}, Value: &Ident{P: Position{2, 29}, Name: "v"}},
				}},
			},
			{
				P:          Position{3, 5},
				IsWildcard: true,
				Body: &Block{P: Position{3, 12}, Stmts: []Stmt{
					&Return{P: Position{3, 14}, Value: &NumberLit{P: Position{3, 21}, Value: 0}},
				}},
			},
		},
	}
	got := collectKinds(root)
	want := []string{
		"*ast.Match",
		"*ast.Ident",
		"*ast.Block", "*ast.Return", "*ast.Ident",
		"*ast.Block", "*ast.Return", "*ast.NumberLit",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("match walk: got %v want %v", got, want)
	}
}

func TestWalk_MatchArmGuardIsVisitedBeforeBody(t *testing.T) {
	root := &MatchExpr{
		P:   Position{1, 1},
		Tag: &Ident{P: Position{1, 7}, Name: "e"},
		Arms: []*MatchExprArm{
			{
				P:           Position{2, 5},
				VariantName: "Some",
				Bindings:    []string{"v"},
				Guard: &Binary{
					P: Position{2, 20}, Op: ">",
					Left:  &Ident{P: Position{2, 20}, Name: "v"},
					Right: &NumberLit{P: Position{2, 24}, Value: 0},
				},
				Body: &Ident{P: Position{2, 30}, Name: "v"},
			},
		},
	}
	got := collectKinds(root)
	want := []string{
		"*ast.MatchExpr",
		"*ast.Ident",                                  // tag
		"*ast.Binary", "*ast.Ident", "*ast.NumberLit", // guard
		"*ast.Ident", // body
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("match-expr walk: got %v want %v", got, want)
	}
}

func TestWalk_ForOptionalSlots(t *testing.T) {
	// Cover the nil-init / nil-step paths so the walker doesn't
	// blow up on while-style for loops.
	root := &For{
		P:    Position{1, 1},
		Cond: &BoolLit{P: Position{1, 8}, Value: true},
		Body: &Block{P: Position{1, 15}, Stmts: []Stmt{
			&Break{P: Position{2, 1}},
		}},
	}
	got := collectKinds(root)
	want := []string{
		"*ast.For",
		"*ast.BoolLit",
		"*ast.Block", "*ast.Break",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("for walk: got %v want %v", got, want)
	}
}

func TestWalkProgram_TopLevelDecls(t *testing.T) {
	p := &Program{
		Funcs: []*FuncDecl{
			{
				P:    Position{1, 1},
				Name: "f",
				Body: &Block{P: Position{1, 15}, Stmts: []Stmt{
					&Return{P: Position{2, 1}, Value: &NumberLit{P: Position{2, 8}, Value: 42}},
				}},
			},
		},
		Consts: []*ConstDecl{
			{P: Position{4, 1}, Name: "K", Value: &NumberLit{P: Position{4, 10}, Value: 7}},
		},
	}
	var got []string
	WalkProgram(p, func(n Node) bool {
		got = append(got, reflect.TypeOf(n).String())
		return true
	})
	want := []string{
		"*ast.FuncDecl", "*ast.Block", "*ast.Return", "*ast.NumberLit",
		"*ast.ConstDecl", "*ast.NumberLit",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("program walk: got %v want %v", got, want)
	}
}

func TestWalkProgram_NilIsNoOp(t *testing.T) {
	var calls int
	WalkProgram(nil, func(Node) bool { calls++; return true })
	if calls != 0 {
		t.Fatalf("nil program should not call fn, got %d", calls)
	}
}

// PositionedSearch is a typical LSP query: "find the deepest node
// whose start position is <= (line, col)". The test asserts Walk
// can drive that search without missing nodes inside a Call's args.
func TestWalk_DeepestNodeAtPosition(t *testing.T) {
	// foo(bar(1), 2)  on line 5.
	// Positions are illustrative.
	inner := &Call{
		P:      Position{5, 5},
		Callee: &Ident{P: Position{5, 5}, Name: "bar"},
		Args:   []Expr{&NumberLit{P: Position{5, 9}, Value: 1}},
	}
	outer := &Call{
		P:      Position{5, 1},
		Callee: &Ident{P: Position{5, 1}, Name: "foo"},
		Args: []Expr{
			inner,
			&NumberLit{P: Position{5, 13}, Value: 2},
		},
	}

	want := Position{5, 9}
	var hit Node
	Walk(outer, func(n Node) bool {
		p := n.Pos()
		if p.Line == want.Line && p.Col <= want.Col {
			hit = n
		}
		return true
	})
	if hit == nil {
		t.Fatalf("no node found at %v", want)
	}
	got, ok := hit.(*NumberLit)
	if !ok || got.Value != 1 {
		t.Fatalf("deepest node at %v: got %T (%+v)", want, hit, hit)
	}
}

func TestWalk_LambdaBody(t *testing.T) {
	// A Lambda's body is part of the enclosing tree: the Call inside
	// must be visited (capability reporting attributes closure bodies
	// to their definition site through this descent).
	root := &Lambda{
		P: Position{1, 1},
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{Expr: &Call{
				P:      Position{1, 5},
				Callee: &Ident{P: Position{1, 5}, Name: "f"},
			}},
		}},
	}
	got := collectKinds(root)
	want := []string{"*ast.Lambda", "*ast.Block", "*ast.ExprStmt", "*ast.Call", "*ast.Ident"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lambda walk: got %v, want %v", got, want)
	}
}
