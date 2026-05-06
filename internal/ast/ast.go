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
type FloatType struct{}
type ArrayType struct{ Elem Type }
type FuncType struct {
	Params []Type
	Result Type
}

func (NumberType) isType()  {}
func (BoolType) isType()    {}
func (VoidType) isType()    {}
func (StringType) isType()  {}
func (FloatType) isType()   {}
func (ArrayType) isType()   {}
func (*FuncType) isType()   {}
func (NumberType) String() string  { return "number" }
func (BoolType) String() string    { return "boolean" }
func (VoidType) String() string    { return "void" }
func (StringType) String() string  { return "string" }
func (FloatType) String() string   { return "float" }
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
	case FloatType:
		_, ok := b.(FloatType)
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
type FloatLit struct {
	P     Position
	Value float64
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
	// IsString is set by the checker when the indexed value is a
	// string rather than an array. The two paths look identical at
	// the AST level but lower differently: arrays read 4 bytes per
	// slot, strings read 1 byte and zero-extend to a number.
	IsString bool
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
	// IsStringConcat is set by the checker when both operands of `+`
	// are strings, so codegen can lower this binary to a runtime call
	// instead of an integer add.
	IsStringConcat bool
	// IsStringCmp is set by the checker when both operands of `==` or
	// `!=` are strings, so codegen can lower it to a content-comparing
	// runtime call instead of a pointer-equality `i32.eq`.
	IsStringCmp bool
	// IsFloat is set by the checker when both operands are floats,
	// so codegen knows to emit f32 instructions instead of i32.
	IsFloat bool
}
type Unary struct {
	P       Position
	Op      string
	Operand Expr
	// IsFloat is set by the checker when the operand is a float,
	// so codegen can pick the f32 form of the operation.
	IsFloat bool
}
type Assign struct {
	P      Position
	Target Expr
	Value  Expr
}

// Ternary models `cond ? then : else`. It's an expression (not a
// statement) so it composes inside arithmetic and assignment.
type Ternary struct {
	P    Position
	Cond Expr
	Then Expr
	Else Expr
	// IsFloat is set by the checker when the result type is `float`,
	// so the WASM backend knows to use `if (result f32)` instead of
	// `if (result i32)`.
	IsFloat bool
}

func (e *NumberLit) Pos() Position { return e.P }
func (e *BoolLit) Pos() Position   { return e.P }
func (e *StringLit) Pos() Position { return e.P }
func (e *FloatLit) Pos() Position  { return e.P }
func (e *Ident) Pos() Position     { return e.P }
func (e *ArrayLit) Pos() Position  { return e.P }
func (e *Index) Pos() Position     { return e.P }
func (e *Call) Pos() Position      { return e.P }
func (e *Binary) Pos() Position    { return e.P }
func (e *Unary) Pos() Position     { return e.P }
func (e *Assign) Pos() Position    { return e.P }
func (e *Ternary) Pos() Position   { return e.P }

func (*NumberLit) isExpr() {}
func (*BoolLit) isExpr()   {}
func (*StringLit) isExpr() {}
func (*FloatLit) isExpr()  {}
func (*Ident) isExpr()     {}
func (*ArrayLit) isExpr()  {}
func (*Index) isExpr()     {}
func (*Call) isExpr()      {}
func (*Binary) isExpr()    {}
func (*Unary) isExpr()     {}
func (*Assign) isExpr()    {}
func (*Ternary) isExpr()   {}

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

// For preserves the C/JS-style three-part for loop so that `continue`
// can jump to the step *before* re-checking the condition.
type For struct {
	P    Position
	Init Stmt // may be nil
	Cond Expr // required
	Step Stmt // may be nil
	Body Stmt
}

type Break struct {
	P Position
}
type Continue struct {
	P Position
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

// Switch dispatches on a single tag expression. Each case lists one or
// more constant match values (no fallthrough — control flows out at the
// end of the case body). A trailing `default` block runs when no case
// matched; it may be nil.
type Switch struct {
	P       Position
	Tag     Expr
	Cases   []*SwitchCase
	Default *Block // may be nil
}

type SwitchCase struct {
	P      Position
	Values []Expr
	Body   *Block
}

func (s *Block) Pos() Position    { return s.P }
func (s *If) Pos() Position       { return s.P }
func (s *While) Pos() Position    { return s.P }
func (s *For) Pos() Position      { return s.P }
func (s *Break) Pos() Position    { return s.P }
func (s *Continue) Pos() Position { return s.P }
func (s *Return) Pos() Position   { return s.P }
func (s *Var) Pos() Position      { return s.P }
func (s *ExprStmt) Pos() Position { return s.P }
func (s *Switch) Pos() Position   { return s.P }

func (*Block) isStmt()    {}
func (*If) isStmt()       {}
func (*While) isStmt()    {}
func (*For) isStmt()      {}
func (*Break) isStmt()    {}
func (*Continue) isStmt() {}
func (*Return) isStmt()   {}
func (*Var) isStmt()      {}
func (*ExprStmt) isStmt() {}
func (*Switch) isStmt()   {}

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
