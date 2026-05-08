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

// NumberType represents a fixed-width signed or unsigned integer.
// The zero value (`NumberType{}`) is `i32`/signed for backward
// compatibility with code written before the sized-integer
// migration. Equality canonicalises Width=0 to 32 so
// `NumberType{}` and `NumberType{Width: 32, Signed: true}` compare
// equal — the source-level keywords `number` and `i32` lower to
// the zero value (cheaper) and the explicitly-typed forms
// respectively.
//
// Width must be one of 8, 16, 32, or 64. Sub-i32 widths lower to
// i32 wasm ops with masking on store; i64 uses native i64 ops.
// Sub-i32 widths are reserved for a follow-up PR — currently only
// 32 and 64 are exercised end-to-end.
//
// Spelling carries the source-level keyword the parser saw
// (`"number"`, `"i32"`, ...). It's purely for the formatter so
// `lang -fmt` round-trips the user's chosen spelling instead of
// always converging on the canonical name. Equality and codegen
// ignore it; the zero value means "no source spelling captured,
// use the canonical name on output".
type NumberType struct {
	Width    int
	Signed   bool
	Spelling string
}
type BoolType struct{}
type VoidType struct{}
type StringType struct{}

// FloatType represents an IEEE-754 binary float. Width is 32 or
// 64; the zero value is f32 to keep `FloatType{}` working
// unchanged after the f64 type was added. Currently only Width=32
// is wired through the backends; f64 is reserved for a follow-up.
//
// Spelling matches NumberType.Spelling — captures the keyword
// the parser saw (`"float"`, `"f32"`, ...) so the formatter can
// preserve it on round-trip.
type FloatType struct {
	Width    int
	Spelling string
}
type ArrayType struct{ Elem Type }

// TupleType is an anonymous heterogeneous tuple — `(i32, string)`,
// `(i32, i32, bool)`, etc. Two tuples are equal when their element
// types match pairwise. Single-element tuples (`(i32,)`) require
// the trailing comma in source so they can't be confused with
// grouping parentheses.
type TupleType struct {
	Elems []Type
}
type FuncType struct {
	Params []Type
	Result Type
}

// StructType is a nominal reference to a top-level `struct` declaration.
// Two StructTypes are equal iff their Name fields match. The actual
// field list lives on the program's StructDecl, looked up via Name.
type StructType struct{ Name string }

// EnumType is a nominal reference to a top-level `enum` declaration,
// optionally with concrete type arguments. `Args` is empty for
// non-generic enums (`Status`); populated for generic instantiations
// (`Option[number]` -> `EnumType{Name: "Option", Args: [Number]}`).
//
// Two EnumTypes are equal when their names match AND their type
// argument lists are pairwise equal — `Option[number]` ≠
// `Option[string]`, even though they share the same EnumDecl.
type EnumType struct {
	Name string
	Args []Type
}

// ParamType is a stand-in for an enum's type parameter inside its
// declaration body — it appears in variant payload type lists
// (`Some(T)` -> the payload type is `ParamType{Name: "T"}`). The
// checker rewrites ParamType references to concrete types when
// it instantiates `Option[number]`, so the type machinery never
// needs to compare two parameters from different scopes.
type ParamType struct{ Name string }

func (NumberType) isType()  {}
func (BoolType) isType()    {}
func (VoidType) isType()    {}
func (StringType) isType()  {}
func (FloatType) isType()   {}
func (ArrayType) isType()   {}
func (TupleType) isType()   {}
func (*FuncType) isType()   {}
func (StructType) isType()  {}
func (EnumType) isType()    {}
func (ParamType) isType()   {}
func (n NumberType) String() string {
	if n.IsSigned() {
		return fmt.Sprintf("i%d", n.NormalWidth())
	}
	return fmt.Sprintf("u%d", n.NormalWidth())
}
func (BoolType) String() string   { return "boolean" }
func (VoidType) String() string   { return "void" }
func (StringType) String() string { return "string" }
func (f FloatType) String() string {
	return fmt.Sprintf("f%d", f.NormalWidth())
}
func (a ArrayType) String() string { return a.Elem.String() + "[]" }
func (t TupleType) String() string {
	out := "("
	for i, e := range t.Elems {
		if i > 0 {
			out += ", "
		}
		out += e.String()
	}
	if len(t.Elems) == 1 {
		// Trailing comma is required in source for unambiguous
		// parsing; mirror it on the way out so re-parsing gives
		// the same shape.
		out += ","
	}
	return out + ")"
}
func (s StructType) String() string { return s.Name }
func (e EnumType) String() string {
	if len(e.Args) == 0 {
		return e.Name
	}
	out := e.Name + "["
	for i, a := range e.Args {
		if i > 0 {
			out += ", "
		}
		out += a.String()
	}
	return out + "]"
}
func (p ParamType) String() string { return p.Name }
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

// NormalWidth returns the canonical bit-width of a NumberType.
// Width=0 (the zero value, used by `NumberType{}` for legacy
// `number` callers) maps to 32 so `NumberType{}` keeps matching
// the explicit `NumberType{Width: 32, Signed: true}` for `i32`.
func (n NumberType) NormalWidth() int {
	if n.Width == 0 {
		return 32
	}
	return n.Width
}

// IsSigned reports whether a NumberType is signed. The zero value
// (Width=0) is signed by convention so legacy `NumberType{}` keeps
// comparing equal to `i32`.
func (n NumberType) IsSigned() bool {
	if n.Width == 0 {
		return true
	}
	return n.Signed
}

// NormalWidth returns the canonical bit-width of a FloatType.
// Width=0 maps to 32 (legacy `float` / `FloatType{}`).
func (f FloatType) NormalWidth() int {
	if f.Width == 0 {
		return 32
	}
	return f.Width
}

// Equal reports whether two types are structurally equal.
func Equal(a, b Type) bool {
	switch x := a.(type) {
	case NumberType:
		y, ok := b.(NumberType)
		return ok && x.NormalWidth() == y.NormalWidth() && x.IsSigned() == y.IsSigned()
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
		y, ok := b.(FloatType)
		return ok && x.NormalWidth() == y.NormalWidth()
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && Equal(x.Elem, y.Elem)
	case TupleType:
		y, ok := b.(TupleType)
		if !ok || len(x.Elems) != len(y.Elems) {
			return false
		}
		for i := range x.Elems {
			if !Equal(x.Elems[i], y.Elems[i]) {
				return false
			}
		}
		return true
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
	case EnumType:
		y, ok := b.(EnumType)
		if !ok || x.Name != y.Name || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !Equal(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case ParamType:
		y, ok := b.(ParamType)
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

// CastExpr is `expr as Type`. The checker requires Target to be a
// numeric type; it lowers to truncation/extension/sign-flip ops in
// the IR. This is the only path between distinct numeric widths
// (i32 ↔ i64 etc.) — implicit numeric widening is rejected per
// docs/LANGUAGE-DIRECTION.md.
type CastExpr struct {
	P      Position
	Inner  Expr
	Target Type
	// InnerType is the resolved type of `Inner`, set by the checker
	// so the IR lowering pass can pick the right truncate / extend
	// op without re-checking. Zero value means the checker didn't
	// resolve it (treat as the legacy i32 default).
	InnerType Type
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
	// IsPipe is set by the parser when this Call was synthesised
	// from a `LHS |> Callee(args...)` pipe expression: Args[0] is
	// the original LHS, Args[1:] are the original explicit args.
	// All later passes treat IsPipe-flagged calls identically to
	// any other Call; only the formatter checks the flag so it
	// can re-render the pipe form on the way out.
	IsPipe bool
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
	// IntWidth is set by the checker for integer binary ops: 32 for
	// i32 (the default), 64 for i64. Sub-i32 widths are reserved.
	// Codegen uses it to pick i32.* vs i64.* instructions.
	IntWidth int
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

// TupleLit is `(e1, e2, …)`. Codegen lowers tuples to heap-allocated
// records — same shape as a struct, but anonymous and addressed by
// position rather than name.
type TupleLit struct {
	P     Position
	Elems []Expr
}

type FieldInit struct {
	Name  string
	Value Expr
}

// EnumLit constructs a tagged-union value: `Circle(3.0)` or bare
// `Red`. EnumName is filled in by the checker once the variant
// resolves; the parser leaves it empty because enum-vs-function
// disambiguation is type-driven. VariantIndex is the runtime
// tag (the variant's position in the EnumDecl's variant list).
type EnumLit struct {
	P            Position
	EnumName     string
	VariantName  string
	VariantIndex int
	Args         []Expr
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
func (e *CastExpr) Pos() Position  { return e.P }
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
func (e *TupleLit) Pos() Position    { return e.P }
func (e *FieldAccess) Pos() Position { return e.P }
func (e *EnumLit) Pos() Position     { return e.P }
func (e *CaptureRef) Pos() Position  { return e.P }
func (e *MakeClosure) Pos() Position { return e.P }

func (*NumberLit) isExpr() {}
func (*CastExpr) isExpr()  {}
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
func (*TupleLit) isExpr()    {}
func (*FieldAccess) isExpr() {}
func (*EnumLit) isExpr()     {}
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

// Match dispatches on a tagged-union value. Unlike Switch (whose
// cases are constant value tests), Match arms are patterns that
// also bind payload fields into local names visible inside the
// arm body. Exhaustiveness is checked at type-check time: every
// variant of the scrutinee's enum type must appear, OR the arm
// list ends with a wildcard pattern (`_`).
type Match struct {
	P    Position
	Tag  Expr
	Arms []*MatchArm
}

// MatchArm is one pattern → body pair. The Bindings are the
// names introduced by the pattern (in declaration order, matching
// the variant's payload positions); each binding's type is the
// matching payload type from the EnumDecl. WildcardPattern arms
// have an empty VariantName and Bindings.
type MatchArm struct {
	P            Position
	VariantName  string   // empty when IsWildcard is true
	Bindings     []string // payload binding names, in payload order
	BindingTypes []Type   // resolved by the checker; same length as Bindings
	IsWildcard   bool     // `_ => …`
	Body         *Block
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
func (s *Match) Pos() Position    { return s.P }
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
func (*Match) isStmt()    {}
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
	// Public marks this declaration as exported from its module.
	// Set by the parser when the source carries `pub function …`.
	// Default false (private) — modload rejects cross-module
	// references to non-public decls before the checker runs.
	Public bool
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
	// Public marks this declaration as exported from its module.
	// Set by the parser when the source carries `pub struct …`.
	// Same semantics as FuncDecl.Public — private structs can't be
	// referenced from other modules.
	Public bool
}

// EnumDecl is a top-level `enum Foo { Bar, Baz(Int), … }`. Each
// variant either has zero positional payload types (a payload-
// less constructor like `Red`) or one or more (`Square(float,
// float)`). Variants are stored in declaration order; the index
// is the runtime tag.
//
// Generic enums declare positional type parameters in brackets
// after the name: `enum Option[T] { Some(T), None }`. Inside a
// variant payload list, references to `T` parse as
// ParamType{Name: "T"}; the checker substitutes them with the
// concrete type arguments at each instantiation. The runtime
// representation is type-erased — payloads are uniform i32
// slots, so generics add no per-instantiation codegen.
type EnumDecl struct {
	P          Position
	Name       string
	TypeParams []string
	Variants   []EnumVariant
	// Public marks the enum as exported across modules. Same
	// semantics as FuncDecl.Public — `pub enum Foo { … }` lets
	// other modules name `Foo`, including its variants in match
	// patterns and constructors.
	Public bool
}

// EnumVariant is one constructor in an EnumDecl. Payloads are
// positional; we don't yet have named-field variants. An empty
// Payloads slice means the variant is constructed by bare name
// (`Red`); a non-empty one means it's called like a function
// (`Square(2.0, 3.0)`).
type EnumVariant struct {
	P        Position
	Name     string
	Payloads []Type
}

type Program struct {
	Funcs   []*FuncDecl
	Structs []*StructDecl
	// Enums lists top-level `enum` declarations in source order.
	// Variant constructors look like calls in the parse tree
	// (`Some(x)`); the checker rewrites them to *EnumLit once
	// the variant is resolved.
	Enums []*EnumDecl
	// Consts lists top-level `const` declarations in source order.
	// The constfold pass evaluates each initialiser, substitutes
	// references throughout the program with the resolved literal,
	// and clears this slice — so the checker / IR lowering / codegen
	// pipeline never sees a ConstDecl.
	Consts []*ConstDecl
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

// ConstDecl is a top-level `const NAME[: T] = expr;` declaration.
// Type is optional — if nil, the constfold pass infers it from the
// resolved value. Value is the parsed initialiser expression: it
// must be a constant expression (literals, references to earlier
// consts, or arithmetic / comparison / logical operations on those).
//
// Public marks the const as exported from its module — same
// semantics as FuncDecl.Public / StructDecl.Public.
type ConstDecl struct {
	P      Position
	Name   string
	Type   Type
	Value  Expr
	Public bool
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
