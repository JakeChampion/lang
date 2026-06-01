package ast

// Node is any AST entity that carries a source position. Every Expr,
// Stmt, and top-level declaration implements it. Type interface
// values don't (types are positionless in this AST), so Walk skips
// them — consumers that need to inspect annotated types do so off
// the parent node directly.
type Node interface {
	Pos() Position
}

// Walk visits every node in the subtree rooted at root in depth-first,
// source-order traversal. fn is called on each node before its
// children; returning false skips descent into that subtree (the
// parent's siblings are still visited). The traversal is read-only;
// fn must not mutate the tree.
//
// Walk is the building block for LSP features that need to locate a
// node by source position (hover, go-to-definition) or enumerate
// references (find-references, rename). It deliberately does not visit
// the Type-shaped fields on expressions and declarations — those are
// positionless and not directly addressable from an editor cursor.
func Walk(root Node, fn func(Node) bool) {
	if root == nil {
		return
	}
	if !fn(root) {
		return
	}
	walkChildren(root, fn)
}

// WalkProgram walks every top-level declaration in p, plus their
// bodies, in source order across funcs / structs / enums / consts.
// Useful as the LSP entry point — there's no single Node for a
// Program (the struct doesn't carry a Position), so callers go
// through this helper instead of Walk(p, ...).
func WalkProgram(p *Program, fn func(Node) bool) {
	if p == nil {
		return
	}
	for _, d := range p.Funcs {
		Walk(d, fn)
	}
	for _, d := range p.Structs {
		Walk(d, fn)
	}
	for _, d := range p.Enums {
		Walk(d, fn)
	}
	for _, d := range p.Unions {
		Walk(d, fn)
	}
	for _, d := range p.Consts {
		Walk(d, fn)
	}
	for _, d := range p.Imports {
		Walk(d, fn)
	}
}

func walkChildren(n Node, fn func(Node) bool) {
	switch x := n.(type) {

	// ---------- Expressions with no Expr children ----------
	case *NumberLit, *BoolLit, *StringLit, *FloatLit, *Ident, *CaptureRef:
		// leaves

	// ---------- Expressions ----------
	case *CastExpr:
		Walk(x.Inner, fn)
	case *FString:
		for _, p := range x.Parts {
			if p.Expr != nil {
				Walk(p.Expr, fn)
			}
		}
		// Desugared is a checker-stamped mirror of Parts; skip
		// it to avoid double-visiting interpolant expressions.
	case *ArrayLit:
		for _, e := range x.Elems {
			Walk(e, fn)
		}
	case *Index:
		Walk(x.Array, fn)
		Walk(x.Idx, fn)
	case *SliceExpr:
		Walk(x.Source, fn)
		if x.Low != nil {
			Walk(x.Low, fn)
		}
		if x.High != nil {
			Walk(x.High, fn)
		}
	case *Call:
		Walk(x.Callee, fn)
		for _, a := range x.Args {
			Walk(a, fn)
		}
	case *Binary:
		Walk(x.Left, fn)
		Walk(x.Right, fn)
	case *Unary:
		Walk(x.Operand, fn)
	case *Assign:
		Walk(x.Target, fn)
		Walk(x.Value, fn)
	case *IfExpr:
		Walk(x.Cond, fn)
		Walk(x.Then, fn)
		Walk(x.Else, fn)
	case *TryOp:
		Walk(x.Inner, fn)
	case *StructLit:
		if x.Base != nil {
			Walk(x.Base, fn)
		}
		for _, f := range x.Fields {
			Walk(f.Value, fn)
		}
	case *TupleLit:
		for _, e := range x.Elems {
			Walk(e, fn)
		}
	case *MapLit:
		for _, e := range x.Entries {
			Walk(e.Key, fn)
			Walk(e.Value, fn)
		}
	case *FieldAccess:
		Walk(x.Target, fn)
	case *EnumLit:
		for _, a := range x.Args {
			Walk(a, fn)
		}
	case *MakeClosure:
		for _, c := range x.Captures {
			Walk(c, fn)
		}
	case *MatchExpr:
		Walk(x.Tag, fn)
		for _, a := range x.Arms {
			if a.Guard != nil {
				Walk(a.Guard, fn)
			}
			Walk(a.Body, fn)
		}

	// ---------- Statements ----------
	case *Block:
		for _, s := range x.Stmts {
			Walk(s, fn)
		}
	case *If:
		Walk(x.Cond, fn)
		Walk(x.Then, fn)
		if x.Else != nil {
			Walk(x.Else, fn)
		}
	case *IfLet:
		Walk(x.Source, fn)
		Walk(x.Then, fn)
		if x.Else != nil {
			Walk(x.Else, fn)
		}
	case *LetElse:
		Walk(x.Source, fn)
		if x.Else != nil {
			Walk(x.Else, fn)
		}
	case *While:
		Walk(x.Cond, fn)
		Walk(x.Body, fn)
	case *For:
		if x.Init != nil {
			Walk(x.Init, fn)
		}
		if x.Cond != nil {
			Walk(x.Cond, fn)
		}
		if x.Step != nil {
			Walk(x.Step, fn)
		}
		Walk(x.Body, fn)
	case *Break, *Continue:
		// leaves
	case *Return:
		if x.Value != nil {
			Walk(x.Value, fn)
		}
	case *Defer:
		Walk(x.Expr, fn)
	case *Arena:
		if x.Body != nil {
			Walk(x.Body, fn)
		}
	case *Var:
		if x.Init != nil {
			Walk(x.Init, fn)
		}
	case *Destructure:
		Walk(x.Init, fn)
	case *ExprStmt:
		Walk(x.Expr, fn)
	case *Switch:
		Walk(x.Tag, fn)
		for _, c := range x.Cases {
			for _, v := range c.Values {
				Walk(v, fn)
			}
			if c.Body != nil {
				Walk(c.Body, fn)
			}
		}
		if x.Default != nil {
			Walk(x.Default, fn)
		}
	case *Match:
		Walk(x.Tag, fn)
		for _, a := range x.Arms {
			if a.Guard != nil {
				Walk(a.Guard, fn)
			}
			if a.Body != nil {
				Walk(a.Body, fn)
			}
		}

	// ---------- Top-level ----------
	case *FuncDecl:
		if x.Body != nil {
			Walk(x.Body, fn)
		}
	case *StructDecl, *EnumDecl, *UnionDecl, *Import:
		// leaves at the AST level (their child types are
		// positionless type references, not Nodes)
	case *ConstDecl:
		if x.Value != nil {
			Walk(x.Value, fn)
		}
	}
}
