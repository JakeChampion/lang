package ast

import "fmt"

type Node interface {
	Pos() Position
}

func Walk(root Node, fn func(Node) bool) {
	if root == nil {
		return
	}
	if !fn(root) {
		return
	}
	walkChildren(root, fn)
}

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

func RewriteProgramExprs(p *Program, fn func(Expr) Expr) {
	if p == nil {
		return
	}
	for _, d := range p.Funcs {
		if d.Body != nil {
			rewriteStmtChildren(d.Body, fn)
		}
	}
	for _, d := range p.Consts {
		if d.Value != nil {
			d.Value = rewriteExpr(d.Value, fn)
		}
	}
}

func RewriteBlockExprs(b *Block, fn func(Expr) Expr) {
	if b != nil {
		rewriteStmtChildren(b, fn)
	}
}

func rewriteExpr(e Expr, fn func(Expr) Expr) Expr {
	if e == nil {
		return nil
	}
	rewriteExprChildren(e, fn)
	return fn(e)
}

func rewriteExprChildren(n Node, fn func(Expr) Expr) {
	switch x := n.(type) {
	case *NumberLit, *BoolLit, *UnitLit, *StringLit, *CharLit, *FloatLit, *Ident, *CaptureRef:
	case *CastExpr:
		x.Inner = rewriteExpr(x.Inner, fn)
	case *DowncastExpr:
		x.Inner = rewriteExpr(x.Inner, fn)
	case *FString:
		for i := range x.Parts {
			if x.Parts[i].Expr != nil {
				x.Parts[i].Expr = rewriteExpr(x.Parts[i].Expr, fn)
			}
		}
		if x.Desugared != nil {
			x.Desugared = rewriteExpr(x.Desugared, fn)
		}
	case *ArrayLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteExpr(x.Elems[i], fn)
		}
	case *Index:
		x.Array = rewriteExpr(x.Array, fn)
		x.Idx = rewriteExpr(x.Idx, fn)
	case *SliceExpr:
		x.Source = rewriteExpr(x.Source, fn)
		if x.Low != nil {
			x.Low = rewriteExpr(x.Low, fn)
		}
		if x.High != nil {
			x.High = rewriteExpr(x.High, fn)
		}
	case *Call:
		x.Callee = rewriteExpr(x.Callee, fn)
		for i := range x.Args {
			x.Args[i] = rewriteExpr(x.Args[i], fn)
		}
	case *Binary:
		switch {
		case x.EqCall != nil:
			rewriteExprChildren(x.EqCall, fn)
		case x.CmpCall != nil:
			rewriteExprChildren(x.CmpCall, fn)
		case x.ArithCall != nil:
			rewriteExprChildren(x.ArithCall, fn)
		default:
			x.Left = rewriteExpr(x.Left, fn)
			x.Right = rewriteExpr(x.Right, fn)
		}
	case *Unary:
		if x.NegCall != nil {
			rewriteExprChildren(x.NegCall, fn)
		} else {
			x.Operand = rewriteExpr(x.Operand, fn)
		}
	case *Assign:
		x.Target = rewriteExpr(x.Target, fn)
		x.Value = rewriteExpr(x.Value, fn)
	case *IfExpr:
		x.Cond = rewriteExpr(x.Cond, fn)
		x.Then = rewriteExpr(x.Then, fn)
		x.Else = rewriteExpr(x.Else, fn)
	case *TryOp:
		x.Inner = rewriteExpr(x.Inner, fn)
	case *StructLit:
		if x.Base != nil {
			x.Base = rewriteExpr(x.Base, fn)
		}
		for i := range x.Fields {
			x.Fields[i].Value = rewriteExpr(x.Fields[i].Value, fn)
		}
	case *TupleLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteExpr(x.Elems[i], fn)
		}
	case *MapLit:
		for i := range x.Entries {
			x.Entries[i].Key = rewriteExpr(x.Entries[i].Key, fn)
			x.Entries[i].Value = rewriteExpr(x.Entries[i].Value, fn)
		}
	case *FieldAccess:
		x.Target = rewriteExpr(x.Target, fn)
	case *EnumLit:
		for i := range x.Args {
			x.Args[i] = rewriteExpr(x.Args[i], fn)
		}
	case *Lambda:
		if x.Body != nil {
			rewriteStmtChildren(x.Body, fn)
		}
	case *MakeClosure:
		for i := range x.Captures {
			x.Captures[i] = rewriteExpr(x.Captures[i], fn)
		}
	case *MatchExpr:
		x.Tag = rewriteExpr(x.Tag, fn)
		for i := range x.Arms {
			if x.Arms[i].Guard != nil {
				x.Arms[i].Guard = rewriteExpr(x.Arms[i].Guard, fn)
			}
			x.Arms[i].Body = rewriteExpr(x.Arms[i].Body, fn)
		}
	case *BlockExpr:
		for _, s := range x.Stmts {
			rewriteStmtChildren(s, fn)
		}
		if x.Tail != nil {
			x.Tail = rewriteExpr(x.Tail, fn)
		}
	case Stmt:
		rewriteStmtChildren(x, fn)
	default:
		panic(unhandledKind("rewriteExprChildren", n))
	}
}

func rewriteStmtChildren(n Node, fn func(Expr) Expr) {
	switch x := n.(type) {
	case *Block:
		for _, s := range x.Stmts {
			rewriteStmtChildren(s, fn)
		}
	case *If:
		x.Cond = rewriteExpr(x.Cond, fn)
		rewriteStmtChildren(x.Then, fn)
		if x.Else != nil {
			rewriteStmtChildren(x.Else, fn)
		}
	case *While:
		x.Cond = rewriteExpr(x.Cond, fn)
		rewriteStmtChildren(x.Body, fn)
	case *Loop:
		rewriteStmtChildren(x.Body, fn)
	case *For:
		if x.Init != nil {
			rewriteStmtChildren(x.Init, fn)
		}
		if x.Cond != nil {
			x.Cond = rewriteExpr(x.Cond, fn)
		}
		if x.Step != nil {
			rewriteStmtChildren(x.Step, fn)
		}
		rewriteStmtChildren(x.Body, fn)
	case *ForEach:
		x.Iter = rewriteExpr(x.Iter, fn)
		rewriteStmtChildren(x.Body, fn)
	case *Return:
		if x.Value != nil {
			x.Value = rewriteExpr(x.Value, fn)
		}
	case *Defer:
		x.Expr = rewriteExpr(x.Expr, fn)
	case *Var:
		if x.Init != nil {
			x.Init = rewriteExpr(x.Init, fn)
		}
	case *Destructure:
		x.Init = rewriteExpr(x.Init, fn)
		for _, sub := range x.Nested {
			if sub != nil {
				rewriteStmtChildren(sub, fn)
			}
		}
	case *ExprStmt:
		x.Expr = rewriteExpr(x.Expr, fn)
	case *Match:
		x.Tag = rewriteExpr(x.Tag, fn)
		for ai := range x.Arms {
			if x.Arms[ai].Guard != nil {
				x.Arms[ai].Guard = rewriteExpr(x.Arms[ai].Guard, fn)
			}
			if x.Arms[ai].Body != nil {
				rewriteStmtChildren(x.Arms[ai].Body, fn)
			}
		}
	case *FuncDecl:
		if x.Body != nil {
			rewriteStmtChildren(x.Body, fn)
		}
	case *Break, *Continue:
	default:
		panic(unhandledKind("rewriteStmtChildren", n))
	}
}

func NodeKinds() []Node {
	return []Node{

		&NumberLit{}, &FloatLit{}, &BoolLit{}, &UnitLit{}, &StringLit{}, &CharLit{},
		&FString{}, &Ident{}, &CaptureRef{}, &ArrayLit{}, &TupleLit{}, &MapLit{},
		&StructLit{}, &EnumLit{}, &Index{}, &SliceExpr{}, &Call{}, &Binary{},
		&Unary{}, &Assign{}, &IfExpr{}, &MatchExpr{}, &BlockExpr{}, &TryOp{},
		&FieldAccess{}, &CastExpr{}, &DowncastExpr{}, &Lambda{}, &MakeClosure{},

		&Block{}, &If{}, &While{}, &Loop{}, &For{}, &ForEach{}, &Break{},
		&Continue{}, &Return{}, &Defer{}, &Var{}, &Destructure{}, &ExprStmt{},
		&Match{}, &FuncDecl{},

		&StructDecl{}, &EnumDecl{}, &UnionDecl{}, &ConstDecl{}, &Import{},
		&TraitDecl{}, &ImplDecl{}, &PubUse{},
	}
}

func walkChildren(n Node, fn func(Node) bool) {
	switch x := n.(type) {

	case *NumberLit, *BoolLit, *UnitLit, *StringLit, *CharLit, *FloatLit, *Ident, *CaptureRef:

	case *CastExpr:
		Walk(x.Inner, fn)
	case *DowncastExpr:
		Walk(x.Inner, fn)
	case *FString:
		for _, p := range x.Parts {
			if p.Expr != nil {
				Walk(p.Expr, fn)
			}
		}
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
	case *Lambda:
		if x.Body != nil {
			Walk(x.Body, fn)
		}
	case *MatchExpr:
		Walk(x.Tag, fn)
		for _, a := range x.Arms {
			if a.Guard != nil {
				Walk(a.Guard, fn)
			}
			Walk(a.Body, fn)
		}
	case *BlockExpr:
		for _, s := range x.Stmts {
			Walk(s, fn)
		}
		if x.Tail != nil {
			Walk(x.Tail, fn)
		}

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
	case *While:
		Walk(x.Cond, fn)
		Walk(x.Body, fn)
	case *Loop:
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
	case *ForEach:
		Walk(x.Iter, fn)
		Walk(x.Body, fn)
	case *Break, *Continue:
	case *Return:
		if x.Value != nil {
			Walk(x.Value, fn)
		}
	case *Defer:
		Walk(x.Expr, fn)
	case *Var:
		if x.Init != nil {
			Walk(x.Init, fn)
		}
	case *Destructure:
		Walk(x.Init, fn)
		for _, sub := range x.Nested {
			if sub != nil {
				Walk(sub, fn)
			}
		}
	case *ExprStmt:
		Walk(x.Expr, fn)
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

	case *FuncDecl:
		if x.Body != nil {
			Walk(x.Body, fn)
		}
	case *StructDecl, *EnumDecl, *UnionDecl, *Import:
	case *TraitDecl, *ImplDecl, *PubUse:
	case *ConstDecl:
		if x.Value != nil {
			Walk(x.Value, fn)
		}
	default:
		panic(unhandledKind("walkChildren", n))
	}
}

func unhandledKind(where string, n Node) string {
	return fmt.Sprintf("ast: %s: unhandled node kind %T (add a case for it)", where, n)
}
