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

// Comment is a `//` line comment captured by the lexer and threaded
// through to consumers like the formatter that want to re-emit
// human-written notes. Text excludes the leading `//` and any
// trailing newline; Pos points at the `//` itself so the formatter
// can decide whether the comment is leading (different line from
// the next statement) or trailing (same line as the previous one).
type Comment struct {
	Pos  Position
	Text string
}

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

// StructType is a nominal reference to a top-level `struct` declaration.
// Two StructTypes are equal iff their Name fields match. The actual
// field list lives on the program's StructDecl, looked up via Name.
type StructType struct{ Name string }

func (NumberType) isType()  {}
func (BoolType) isType()    {}
func (VoidType) isType()    {}
func (StringType) isType()  {}
func (FloatType) isType()   {}
func (ArrayType) isType()   {}
func (*FuncType) isType()   {}
func (StructType) isType()  {}
func (NumberType) String() string  { return "number" }
func (BoolType) String() string    { return "boolean" }
func (VoidType) String() string    { return "void" }
func (StringType) String() string  { return "string" }
func (FloatType) String() string   { return "float" }
func (a ArrayType) String() string { return a.Elem.String() + "[]" }
func (s StructType) String() string { return s.Name }
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
	case StructType:
		y, ok := b.(StructType)
		return ok && x.Name == y.Name
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

// StructLit constructs a struct value: `Foo { x: 1, y: 2 }`.
// Fields may appear in any order; the checker reorders them to match
// the declaration so codegen can use fixed offsets.
type StructLit struct {
	P        Position
	TypeName string
	Fields   []FieldInit
}

type FieldInit struct {
	Name  string
	Value Expr
}

// FieldAccess reads a field off a struct value: `p.x`. Codegen lowers
// this to `i32.load (p + offset)` once the checker has resolved the
// field's offset on the StructDecl.
type FieldAccess struct {
	P      Position
	Target Expr
	Field  string
}

// CaptureRef is a synthetic expression introduced by closure
// conversion. Inside a hoisted local function's body, references to
// captured outer-scope variables are rewritten from `*Ident` to
// `*CaptureRef`, which codegen lowers as a load from the function's
// hidden env parameter at the recorded offset.
type CaptureRef struct {
	P      Position
	Name   string
	Offset int  // byte offset into the env block
	Type   Type // captured variable's static type
}

// MakeClosure is a synthetic expression introduced by closure
// conversion. It marks the def site of a nested function: codegen
// allocates an env block, evaluates each `Captures` expression and
// stores it at the matching offset, allocates an 8-byte closure
// pair `{fn_idx, env_ptr}`, and returns the closure pointer. The
// FuncName resolves to the hoisted top-level function the
// FuncIndex selects in the funcref table.
type MakeClosure struct {
	P         Position
	FuncName  string
	FuncIndex int
	Captures  []Expr
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
func (e *Assign) Pos() Position      { return e.P }
func (e *Ternary) Pos() Position     { return e.P }
func (e *StructLit) Pos() Position   { return e.P }
func (e *FieldAccess) Pos() Position { return e.P }
func (e *CaptureRef) Pos() Position  { return e.P }
func (e *MakeClosure) Pos() Position { return e.P }

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
func (*Assign) isExpr()      {}
func (*Ternary) isExpr()     {}
func (*StructLit) isExpr()   {}
func (*FieldAccess) isExpr() {}
func (*CaptureRef) isExpr()  {}
func (*MakeClosure) isExpr() {}

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
func (s *FuncDecl) Pos() Position { return s.P }

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
func (*FuncDecl) isStmt() {} // legal as a stmt only when IsLocal is true

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
	// Receiver, when non-nil, marks this declaration as a method on
	// the struct type Receiver.Type.(StructType).Name. The checker
	// hoists methods into top-level functions under the mangled name
	// `__method_<Type>_<Name>` and rewrites `expr.Method(args)` call
	// sites to `__method_<Type>_<Method>(expr, args)` so codegen
	// never has to know about methods.
	Receiver *Param
	// IsLocal is true for functions declared as a statement inside
	// another function's body. Closure conversion at codegen time
	// hoists these to top-level entries and rewrites captured-var
	// references to read from a synthetic env argument.
	IsLocal bool
	// Captures is filled by the checker for IsLocal functions: each
	// entry names an outer-scope variable that the body reads, with
	// the variable's static type. The closure-conversion pass uses
	// this list to size the env block and to know how to materialise
	// each capture at the def site.
	Captures []Param
}

// StructDecl is a top-level `struct` declaration. Fields are stored in
// declaration order, which is also the layout order in memory: each
// field occupies 4 bytes and lives at offset 4*index from the struct's
// base pointer.
type StructDecl struct {
	P      Position
	Name   string
	Fields []Param
}

type Program struct {
	Funcs   []*FuncDecl
	Structs []*StructDecl
	// Imports lists every top-level `import "<path>";` declaration
	// in source order. The driver loads the referenced files,
	// mangles their decls under each module's local name, and
	// stitches the combined program before the checker runs.
	// Single-file programs leave this empty.
	Imports []*Import
	// Comments lists every `//` line comment the lexer collected,
	// in source order. Most consumers (checker, IR lowering,
	// codegen) ignore this field; the formatter walks it alongside
	// the AST to re-emit comments at their original positions.
	Comments []Comment
}

// Import is a top-level `import "<path>";` declaration. Path is the
// raw string-literal text from the source (typically a relative
// path like "./util" or "./math/vec"); LocalName is derived from
// the path's basename and is what qualified calls use as the
// module prefix (`util.fn(args)`).
type Import struct {
	P         Position
	Path      string
	LocalName string
}
