// Package ast defines the abstract syntax tree for the lang language.
//
// AST kinds are split into three sealed interfaces — Expr, Stmt and Type —
// each with an unexported tag method so foreign packages can match on them
// with a type switch but cannot add new variants.
package ast

import "fmt"

// Position is a 1-based line/column pair used for diagnostics.
type Position struct {
	Line int
	Col  int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// ---------- Types ----------

type Type interface {
	String() string
	isType()
}

type NumberType struct{}
type BoolType struct{}
type VoidType struct{}
type StringType struct{}
type ArrayType struct{ Elem Type }
type FuncType struct {
	Params []Type
	Result Type
}

func (NumberType) isType()  {}
func (BoolType) isType()    {}
func (VoidType) isType()    {}
func (StringType) isType()  {}
func (ArrayType) isType()   {}
func (*FuncType) isType()   {}
func (NumberType) String() string  { return "number" }
func (BoolType) String() string    { return "boolean" }
func (VoidType) String() string    { return "void" }
func (StringType) String() string  { return "string" }
func (a ArrayType) String() string { return a.Elem.String() + "[]" }
func (f *FuncType) String() string {
	out := "("
	for i, p := range f.Params {
		if i > 0 {
			out += ", "
		}
		out += p.String()
	}
	out += ") => " + f.Result.String()
	return out
}

// Equal reports whether two types are structurally equal.
func Equal(a, b Type) bool {
	switch x := a.(type) {
	case NumberType:
		_, ok := b.(NumberType)
		return ok
	case BoolType:
		_, ok := b.(BoolType)
		return ok
	case VoidType:
		_, ok := b.(VoidType)
		return ok
	case StringType:
		_, ok := b.(StringType)
		return ok
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && Equal(x.Elem, y.Elem)
	case *FuncType:
		y, ok := b.(*FuncType)
		if !ok || len(x.Params) != len(y.Params) || !Equal(x.Result, y.Result) {
			return false
		}
		for i := range x.Params {
			if !Equal(x.Params[i], y.Params[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// ---------- Expressions ----------

type Expr interface {
	Pos() Position
	isExpr()
}

type NumberLit struct {
	P     Position
	Value int64
}
type BoolLit struct {
	P     Position
	Value bool
}
type StringLit struct {
	P     Position
	Value string
}
type Ident struct {
	P    Position
	Name string
}
type ArrayLit struct {
	P     Position
	Elems []Expr
}
type Index struct {
	P     Position
	Array Expr
	Idx   Expr
}
type Call struct {
	P      Position
	Callee Expr
	Args   []Expr
}
type Binary struct {
	P           Position
	Op          string
	Left, Right Expr
}
type Unary struct {
	P       Position
	Op      string
	Operand Expr
}
type Assign struct {
	P      Position
	Target Expr
	Value  Expr
}

func (e *NumberLit) Pos() Position { return e.P }
func (e *BoolLit) Pos() Position   { return e.P }
func (e *StringLit) Pos() Position { return e.P }
func (e *Ident) Pos() Position     { return e.P }
func (e *ArrayLit) Pos() Position  { return e.P }
func (e *Index) Pos() Position     { return e.P }
func (e *Call) Pos() Position      { return e.P }
func (e *Binary) Pos() Position    { return e.P }
func (e *Unary) Pos() Position     { return e.P }
func (e *Assign) Pos() Position    { return e.P }

func (*NumberLit) isExpr() {}
func (*BoolLit) isExpr()   {}
func (*StringLit) isExpr() {}
func (*Ident) isExpr()     {}
func (*ArrayLit) isExpr()  {}
func (*Index) isExpr()     {}
func (*Call) isExpr()      {}
func (*Binary) isExpr()    {}
func (*Unary) isExpr()     {}
func (*Assign) isExpr()    {}

// ---------- Statements ----------

type Stmt interface {
	Pos() Position
	isStmt()
}

type Block struct {
	P     Position
	Stmts []Stmt
}
type If struct {
	P    Position
	Cond Expr
	Then Stmt
	Else Stmt // may be nil
}
type While struct {
	P    Position
	Cond Expr
	Body Stmt
}
type Return struct {
	P     Position
	Value Expr // may be nil
}
type Var struct {
	P    Position
	Name string
	Type Type // may be nil — inferred
	Init Expr
}
type ExprStmt struct {
	P    Position
	Expr Expr
}

func (s *Block) Pos() Position    { return s.P }
func (s *If) Pos() Position       { return s.P }
func (s *While) Pos() Position    { return s.P }
func (s *Return) Pos() Position   { return s.P }
func (s *Var) Pos() Position      { return s.P }
func (s *ExprStmt) Pos() Position { return s.P }

func (*Block) isStmt()    {}
func (*If) isStmt()       {}
func (*While) isStmt()    {}
func (*Return) isStmt()   {}
func (*Var) isStmt()      {}
func (*ExprStmt) isStmt() {}

// ---------- Top level ----------

type Param struct {
	Name string
	Type Type
}

type FuncDecl struct {
	P          Position
	Name       string
	Params     []Param
	ReturnType Type
	Body       *Block
}

type Program struct {
	Funcs []*FuncDecl
}
