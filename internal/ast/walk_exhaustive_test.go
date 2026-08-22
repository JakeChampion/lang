package ast

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// nodeKinds lists one zero instance of every concrete type implementing
// Node. TestNodeKindsRegistryMatchesPackage keeps it in step with the
// package by deriving the same set from the source, so a new Expr / Stmt /
// declaration cannot reach the traversals below without being listed here
// first — which is what turns "someone added a node kind" from a silent
// no-op in a hand-written switch (#7042, #7149) into a red test.
var nodeKinds = []Node{
	// Expressions.
	&NumberLit{}, &FloatLit{}, &BoolLit{}, &UnitLit{}, &StringLit{}, &CharLit{},
	&FString{}, &Ident{}, &CaptureRef{}, &ArrayLit{}, &TupleLit{}, &MapLit{},
	&StructLit{}, &EnumLit{}, &Index{}, &SliceExpr{}, &Call{}, &Binary{},
	&Unary{}, &Assign{}, &IfExpr{}, &MatchExpr{}, &BlockExpr{}, &TryOp{},
	&FieldAccess{}, &CastExpr{}, &DowncastExpr{}, &Lambda{}, &MakeClosure{},

	// Statements.
	&Block{}, &If{}, &While{}, &Loop{}, &For{}, &ForEach{}, &Break{},
	&Continue{}, &Return{}, &Defer{}, &Var{}, &Destructure{}, &ExprStmt{},
	&Match{}, &FuncDecl{},

	// Declarations.
	&StructDecl{}, &EnumDecl{}, &UnionDecl{}, &ConstDecl{}, &Import{},
	&TraitDecl{}, &ImplDecl{}, &PubUse{},
}

// walkSkips names the Expr-, Stmt- and *Block-typed fields Walk deliberately
// does not descend into, with the reason. TestWalkVisitsEveryChildSlot
// requires every other such field to be reached, and fails on an entry that
// IS reached — so the table cannot rot into a licence for whatever the
// traversal happens to miss.
var walkSkips = map[string]string{
	"FString.Desugared":     "checker-stamped mirror of Parts; walking both double-visits every interpolant",
	"Binary.CheckedLowered": "checker-stamped desugar, spliced in by RewriteProgramExprs before any later pass sees the Binary",
	"TryOp.Lowered":         "checker-stamped desugar, spliced in by RewriteProgramExprs before any later pass sees the TryOp",
	"Loop.TodoMsg":          "the `todo(\"msg\")` message, read by the checker rather than evaluated as an expression",
	"ForEach.RangeHigh":     "range bound consumed by the ForEach desugars, which run before any Walk consumer",
}

func nodeName(n Node) string { return reflect.TypeOf(n).Elem().Name() }

// TestNodeKindsRegistryMatchesPackage derives every Node implementor from
// the package source (each has a `func (*X) Pos() Position`) and requires
// nodeKinds to hold exactly that set.
func TestNodeKindsRegistryMatchesPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fromSource := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range parsed.Decls {
			fd, ok := decl.(*goast.FuncDecl)
			if !ok || fd.Name.Name != "Pos" || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			star, ok := fd.Recv.List[0].Type.(*goast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*goast.Ident); ok {
				fromSource[id.Name] = true
			}
		}
	}
	if len(fromSource) == 0 {
		t.Fatal("found no Pos methods in the package source: the derivation is broken, not the registry")
	}
	registered := map[string]bool{}
	for _, n := range nodeKinds {
		name := nodeName(n)
		if registered[name] {
			t.Errorf("nodeKinds lists %s twice", name)
		}
		registered[name] = true
	}
	for name := range fromSource {
		if !registered[name] {
			t.Errorf("ast.%s implements Node but is missing from nodeKinds — add it there, then give "+
				"it a case in walkChildren, rewriteExprChildren / rewriteStmtChildren and CloneStmt / CloneExpr",
				name)
		}
	}
	for name := range registered {
		if !fromSource[name] {
			t.Errorf("nodeKinds lists ast.%s, which no longer implements Node — drop it", name)
		}
	}
}

// TestWalkHandlesEveryNodeKind proves every registered kind has a case in
// walkChildren: the default arm panics, so a missing one fails here rather
// than silently skipping the subtree at some caller.
func TestWalkHandlesEveryNodeKind(t *testing.T) {
	for _, kind := range nodeKinds {
		t.Run(nodeName(kind), func(t *testing.T) {
			Walk(populate(t, kind, true), func(Node) bool { return true })
		})
	}
}

// TestRewriteChildrenHandlesEveryNodeKind is TestWalkHandlesEveryNodeKind
// for the RewriteProgramExprs traversal, whose two switches enumerate the
// same union a second time.
func TestRewriteChildrenHandlesEveryNodeKind(t *testing.T) {
	for _, kind := range nodeKinds {
		t.Run(nodeName(kind), func(t *testing.T) {
			n := populate(t, kind, true)
			switch x := n.(type) {
			case Stmt:
				rewriteStmtChildren(x, func(e Expr) Expr { return e })
			case Expr:
				rewriteExprChildren(x, func(e Expr) Expr { return e })
			}
		})
	}
}

// TestWalkVisitsEveryChildSlot requires every Expr-, Stmt- and *Block-typed
// field of every node kind to be reached by Walk, or listed in walkSkips.
// A case that exists but forgot one of its node's slots fails here — the
// half of the bug a "is there a case for this type" check cannot see.
func TestWalkVisitsEveryChildSlot(t *testing.T) {
	for _, kind := range nodeKinds {
		t.Run(nodeName(kind), func(t *testing.T) {
			n, slots := populateSlots(t, kind, false)
			seen := map[Node]bool{}
			Walk(n, func(v Node) bool { seen[v] = true; return true })
			for ptr, field := range slots {
				key := nodeName(kind) + "." + field
				_, skipped := walkSkips[key]
				switch {
				case seen[ptr] && skipped:
					t.Errorf("walkSkips lists %s but Walk visits it — drop the entry", key)
				case !seen[ptr] && !skipped:
					t.Errorf("Walk does not descend into %s — add it to walkChildren, or to "+
						"walkSkips with the reason", key)
				}
			}
		})
	}
}

// TestWalkSkipsNameRealFields keeps walkSkips exact in the other direction:
// a renamed or deleted field must take its entry with it.
func TestWalkSkipsNameRealFields(t *testing.T) {
	for key := range walkSkips {
		typeName, field, _ := strings.Cut(key, ".")
		found := false
		for _, kind := range nodeKinds {
			if nodeName(kind) != typeName {
				continue
			}
			if _, ok := reflect.TypeOf(kind).Elem().FieldByName(field); ok {
				found = true
			}
		}
		if !found {
			t.Errorf("walkSkips names %s, which is not a field of any node kind", key)
		}
	}
}

// TestCloneCopiesEverythingWalkReaches states the contract that stops a
// clone from sharing mutable structure with its source: everything Walk can
// reach from a node must be freshly allocated by the clone, and the clone
// must have the same shape. That is exactly the invariant #7042 / #7149
// broke — a missing arm returned the node itself, so substituting types into
// one instantiation wrote through every other. Anything Walk does not reach
// is shared by design, and no pass that traverses via Walk can observe it.
func TestCloneCopiesEverythingWalkReaches(t *testing.T) {
	for _, kind := range nodeKinds {
		name := nodeName(kind)
		t.Run(name, func(t *testing.T) {
			n := populate(t, kind, true)
			var cloned Node
			switch x := n.(type) {
			case Stmt:
				cloned = CloneStmt(x)
			case Expr:
				cloned = CloneExpr(x)
			default:
				t.Skipf("%s is a declaration; the cloners take Stmt / Expr", name)
			}
			origin, copies := walkSeq(n), walkSeq(cloned)
			if len(origin) != len(copies) {
				t.Fatalf("clone reaches %d nodes, original reaches %d — the clone dropped or added children",
					len(copies), len(origin))
			}
			shared := map[Node]bool{}
			for _, v := range origin {
				shared[v] = true
			}
			for i, v := range copies {
				if want, got := reflect.TypeOf(origin[i]), reflect.TypeOf(v); want != got {
					t.Fatalf("clone node %d is %v, original is %v", i, got, want)
				}
				if shared[v] {
					t.Errorf("clone shares %T with its source — deep-copy it where %s is cloned", v, name)
				}
			}
		})
	}
}

func walkSeq(n Node) []Node {
	var out []Node
	Walk(n, func(v Node) bool { out = append(out, v); return true })
	return out
}

// populate builds an instance of kind with every child slot filled by a
// distinct sentinel node. deep also fills the node slots nested inside
// element structs (a match arm's guard, a struct literal's field values).
func populate(t *testing.T, kind Node, deep bool) Node {
	t.Helper()
	n, _ := populateSlots(t, kind, deep)
	return n
}

func populateSlots(t *testing.T, kind Node, deep bool) (Node, map[Node]string) {
	t.Helper()
	slots := map[Node]string{}
	v := reflect.New(reflect.TypeOf(kind).Elem())
	fillStruct(v.Elem(), "", deep, 0, slots)
	n, ok := v.Interface().(Node)
	if !ok {
		t.Fatalf("%T is not a Node", v.Interface())
	}
	return n, slots
}

var (
	exprType  = reflect.TypeOf((*Expr)(nil)).Elem()
	stmtType  = reflect.TypeOf((*Stmt)(nil)).Elem()
	nodeType  = reflect.TypeOf((*Node)(nil)).Elem()
	blockType = reflect.TypeOf((*Block)(nil))
)

// fillStruct assigns a sentinel to every Expr-, Stmt- and *Block-typed field
// of v, recording it under its path. Stmt slots take a *Block: the interface
// admits any statement, but the slots a parser only ever fills with a block
// are asserted to be one by their consumers.
func fillStruct(v reflect.Value, path string, deep bool, depth int, slots map[Node]string) {
	rt := v.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue
		}
		fv, name := v.Field(i), f.Name
		if path != "" {
			name = path + "." + f.Name
		}
		switch {
		case f.Type == exprType:
			fv.Set(reflect.ValueOf(sentinelExpr(name, slots)))
		case f.Type == stmtType || f.Type == blockType:
			fv.Set(reflect.ValueOf(sentinelBlock(name, slots)))
		case f.Type.Kind() == reflect.Slice:
			elem := f.Type.Elem()
			switch {
			case elem == exprType:
				fv.Set(reflect.Append(fv, reflect.ValueOf(sentinelExpr(name+"[0]", slots))))
			case elem == stmtType || elem == blockType:
				fv.Set(reflect.Append(fv, reflect.ValueOf(sentinelBlock(name+"[0]", slots))))
			case deep && depth < 2 && holdsNodeSlot(elem):
				el := newElem(elem)
				fillStruct(indirect(el), name+"[0]", deep, depth+1, slots)
				fv.Set(reflect.Append(fv, el))
			}
		case deep && depth < 2 && f.Type.Kind() == reflect.Ptr && f.Type.Implements(nodeType):
			// A concrete node pointer (Block.Sugar, Lambda.Synthetic,
			// ForEach.Pattern): allocate it empty. Filling it as well
			// would recurse without end through the mutually-referential
			// kinds.
			fv.Set(reflect.New(f.Type.Elem()))
		}
	}
}

func indirect(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Ptr {
		return v.Elem()
	}
	return v
}

func newElem(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem())
	}
	return reflect.New(t).Elem()
}

// holdsNodeSlot reports whether t is a struct (or pointer to one) with an
// Expr / Stmt / *Block field — the element types worth filling, e.g.
// FieldInit, MapEntry, MatchArm.
func holdsNodeSlot(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		switch t.Field(i).Type {
		case exprType, stmtType, blockType:
			return true
		}
	}
	return false
}

func sentinelExpr(path string, slots map[Node]string) Expr {
	s := &Ident{Name: "sentinel:" + path}
	slots[s] = path
	return s
}

func sentinelBlock(path string, slots map[Node]string) *Block {
	s := &Block{}
	slots[s] = path
	return s
}
