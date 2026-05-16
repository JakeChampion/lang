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
// WidthPtr is the sentinel value used by `NumberType.Width`
// to mark the target-aware native-pointer-width integer
// (`usize`). Backends resolve it to 4 bytes on wasm32 and 8
// bytes on arm64 / x86-64. Mirrored by `ir.WidthPtr` for the
// codegen side; kept in the ast package so type-level code
// doesn't need to import ir.
const WidthPtr = -1

type NumberType struct {
	Width    int
	Signed   bool
	Spelling string
	// Polymorphic is set on the NumberType returned for an
	// unsettled NumberLit ("polymorphic numeric literal" — `1`,
	// `42`). It flows through `assignable` to any concrete
	// integer type and through `commonIntegerWidth` so the
	// other operand's width wins. Once the literal is settled
	// (the surrounding context demands a concrete width), the
	// checker stamps `Width` on the NumberLit AST node and
	// returns the concrete NumberType from the affected call
	// sites. Polymorphic propagates through ast.Equal as a
	// "matches anything int" wildcard.
	Polymorphic bool
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
	// Polymorphic flags an unsettled `*ast.FloatLit` so the
	// checker can propagate it to the surrounding expected type
	// the same way NumberType.Polymorphic works for ints.
	Polymorphic bool
}
type ArrayType struct{ Elem Type }

// SliceType is a non-owning view into an Array<T>. Spelled `[T]`
// in source — distinct from owned `T[]` so the API surface
// signals "this borrows" without a borrow checker. Codegen
// lowers a slice value to a pointer to an 8-byte heap struct
// `{data_ptr: i32, len: i32}` — `data_ptr` aliases the parent
// array's storage, so a slice + its parent share the parent's
// lifetime. Bump-allocator semantics make that contract trivial:
// everything alive at the end of the arena lives until the
// arena resets.
type SliceType struct{ Elem Type }

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

// StructType is a nominal reference to a top-level `struct`
// declaration, optionally with concrete type arguments. `Args`
// is empty for non-generic structs (`Point`); populated for
// generic instantiations (`Pair[i32, string]` →
// `StructType{Name: "Pair", Args: [i32, string]}`).
//
// Two StructTypes are equal when their names match AND their
// type argument lists are pairwise equal — same shape as
// EnumType. The monomorphisation pass mangles populated-Args
// references into a flat name (`Pair__i32__string`) before any
// later stage sees them, so codegen / interp / printer keep
// their existing "names match" assumption.
type StructType struct {
	Name string
	Args []Type
}

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
func (SliceType) isType()   {}
func (TupleType) isType()   {}
func (*FuncType) isType()   {}
func (StructType) isType()  {}
func (EnumType) isType()    {}
func (ParamType) isType()   {}
func (n NumberType) String() string {
	if n.IsPointerWidth() {
		return "usize"
	}
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
func (s SliceType) String() string { return "[" + s.Elem.String() + "]" }
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
func (s StructType) String() string {
	if len(s.Args) == 0 {
		return s.Name
	}
	out := s.Name + "["
	for i, a := range s.Args {
		if i > 0 {
			out += ", "
		}
		out += a.String()
	}
	return out + "]"
}
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
// Width=WidthPtr (-1) is the target-aware usize sentinel — it
// has no canonical width at the AST layer; backends pick 32 or
// 64 at codegen time. Returning -1 here lets callers detect the
// pointer-width case without colliding with a real bit-width.
func (n NumberType) NormalWidth() int {
	if n.Width == 0 {
		return 32
	}
	return n.Width
}

// IsPointerWidth reports whether the type is the target-aware
// usize. Backends ask this before resolving widths; the IR's
// equivalent on the operand side is `Op.Width == WidthPtr`.
func (n NumberType) IsPointerWidth() bool {
	return n.Width == WidthPtr
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

// ElemSizeBytes returns the in-memory footprint, in bytes, of a
// single element of `t` when stored in an array / slice on
// wasm32 (4-byte pointers). Sub-i32 integers (`u8`/`i8` use 1
// byte, `u16`/`i16` use 2 bytes) and i64 / u64 / f64 use 8
// bytes. Pointer-shaped types (string / Array / struct / enum
// / tuple / slice) return 4 — they hold a heap pointer that
// fits in i32 on wasm32. For native arm64 (where heap pointers
// are 8 bytes), use `ElemSizeBytesFor(t, ptrW)` instead.
func ElemSizeBytes(t Type) int {
	return ElemSizeBytesFor(t, 4)
}

// ElemSizeBytesFor is the target-aware variant. `ptrW` is the
// pointer width in bytes for the current target (4 on wasm32,
// 8 on arm64). Pointer-shaped types return `ptrW` so their full
// heap address survives on arm64-darwin (heap >= 4 GiB). Scalar
// types ignore ptrW.
//
// `StringType` is special-cased on wasm32 (ptrW=4): a string
// element is two i32 slots `(data, len)` under the two-word
// ABI, so the stride is 8 — not `ptrW=4`. On natives the
// existing LSB-tagged single-slot form stays one 8-byte
// pointer slot, so stride is still 8 there too. Both targets
// converge on 8-byte string-element stride; centralising the
// decision here keeps it consistent with `payloadSlotSize`
// in the IR.
func ElemSizeBytesFor(t Type, ptrW int) int {
	switch x := t.(type) {
	case NumberType:
		switch x.NormalWidth() {
		case 8:
			return 1
		case 16:
			return 2
		case 64:
			return 8
		}
		return 4
	case FloatType:
		if x.NormalWidth() == 64 {
			return 8
		}
		return 4
	case StringType:
		if UseTwoWordStrings(ptrW) {
			return 2 * ptrW
		}
		return ptrW
	}
	return ptrW
}

// UseTwoWordStrings reports whether the target whose pointer
// width is `ptrW` carries strings on the operand stack as a
// `(data, len)` two-word pair (vs the legacy single LSB-tagged
// pointer slot). Wasm32 (`ptrW == 4`) is always two-word. The
// arm64 native flip is activated by setting `TwoWordOverride`
// to true before lowering — that path is in progress on
// `claude/sso-native-flip-arm64`; see
// `docs/SSO-NATIVE-FLIP-STATUS.md`.
//
// Lives in the `ast` package because both `internal/ir` and
// `internal/ast`'s own `ElemSizeBytesFor` need to consult it.
// The companion `(b *builder) twoWordStrings()` method in
// `ir.go` calls into this for builder-level checks.
func UseTwoWordStrings(ptrW int) bool {
	if ptrW == 4 {
		return true
	}
	return TwoWordOverride
}

// TwoWordOverride opts a non-wasm target (ptrW != 4) into the
// two-word string ABI. Used by the arm64 native flip during
// the in-progress migration (`docs/SSO-NATIVE-FLIP-STATUS.md`).
// Set to true before `ir.LowerWith` runs; reset after. Single-
// threaded compiler — no race concern.
var TwoWordOverride bool

// IsPointerType reports whether values of `t` are represented
// as heap pointers in the compiled code — so the slot holding
// the value must be pointer-width (4 on wasm32, 8 on arm64).
// Used by IR / codegen to size enum payloads, struct fields,
// array elements, and closure captures correctly on each
// target.
func IsPointerType(t Type) bool {
	switch t.(type) {
	case StringType, ArrayType, SliceType, TupleType,
		StructType, EnumType:
		return true
	case *FuncType:
		return true
	}
	return false
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
	case SliceType:
		y, ok := b.(SliceType)
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
		if !ok || x.Name != y.Name || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !Equal(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
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
	// Width is set by the checker once the literal's type has
	// been resolved. 0 means "default i32" for backwards
	// compatibility (the ir's NumberLit lowering treats 0 the
	// same as 32). Polymorphic literals in expected-type
	// context pick up the expected width here so the IR can
	// emit `i64.const`/`i32.const` correctly without adding
	// implicit widening.
	Width int
	// IsUnsigned tracks whether the resolved type was a `u32`
	// or `u64` (vs `i32`/`i64`). It doesn't affect how the
	// literal itself is emitted — bit pattern is identical —
	// but `*ast.CastExpr.InnerType` and the checker's record
	// of the literal's type need to know.
	IsUnsigned bool
	// IsFloat is set when a polymorphic integer literal got
	// settled to a float type via settleFloat (e.g. `var r:
	// f32 = 0`, `f32_param * 2`, `r <= 0` where r is f32).
	// FloatWidth records the destination float width; the IR
	// emits OpConstF32 / OpConstF64 with float64(Value) instead
	// of the integer-const path. Only set on otherwise-
	// polymorphic literals — a typed-suffix `42i64` is locked
	// to its integer type and won't be promoted here.
	IsFloat    bool
	FloatWidth int
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

// FString is an interpolated string literal — `f"hello {x}"`.
// Parts alternates literal segments and interpolant expressions
// in source order. The empty list means an empty f-string `f""`.
//
// The checker types FString as `string` and stamps `Desugared`
// with the equivalent `+`-chain of string operations
// (`<lit> + (<expr>).to_string() + …`) — that way method-call
// dispatch info for the synthesised `.to_string()` calls gets
// resolved alongside everything else, and the IR can lower the
// chain via the regular Binary-on-strings path. The formatter
// reads `Parts` directly so it can rebuild the original `f"..."`
// syntax on round-trip.
type FString struct {
	P         Position
	Parts     []FStringPart
	Desugared Expr
}

// FStringPart is one piece of an f-string: either a literal
// string segment (Expr is nil) or an interpolant expression
// (Lit is empty). Empty leading / trailing literal segments
// are not stored; an FString with N interpolants and M literal
// segments has N+M parts.
type FStringPart struct {
	Lit  string
	Expr Expr
}
type FloatLit struct {
	P     Position
	Value float64
	// Width is set by the checker once a concrete float type is
	// known (`var x: f64 = 1.5` → 64). 0 means "default f32" for
	// backwards compatibility.
	Width int
}
type Ident struct {
	P    Position
	Name string
}
type ArrayLit struct {
	P     Position
	Elems []Expr
	// ElemType is set by the checker once each element is typed
	// (or settled, for polymorphic numeric literals). The IR uses
	// it to pick a stride (1 byte for `[u8]` / `[i8]`, 2 for
	// `[u16]` / `[i16]`, 4 for `[i32]` / `[u32]` / `[f32]` /
	// pointers, 8 for `[i64]` / `[u64]` / `[f64]`) and to choose
	// between i32.store / i32.store8 / i32.store16 / i64.store /
	// f32.store / f64.store. nil falls back to the historical
	// 4-byte-per-element layout.
	ElemType Type
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
	// IsSlice is set by the checker when the indexed value is a
	// slice (`[T]`) rather than an owned array. Slice indexing
	// goes through one extra level of indirection — the slice
	// holds a `(data_ptr, len)` struct, so accessing element i
	// loads data_ptr first and then offsets into the parent
	// array's storage.
	IsSlice bool
	// ElemType is set by the checker for array / slice indexing
	// once the element type has been resolved. Used by the IR
	// to pick the right stride + load width (1 byte for u8, 4
	// for i32, etc.). nil falls back to the historical 4-byte
	// stride.
	ElemType Type
}

// SliceExpr is `arr[a:b]`, `arr[a:]`, or `arr[:b]` — produces a
// non-owning view that shares the parent array's storage. Either
// bound may be nil for "use 0" (low) or "use len(arr)" (high).
// Source-level support for unbounded forms (`arr[:]`) is reserved.
type SliceExpr struct {
	P      Position
	Source Expr
	Low    Expr // nil = 0
	High   Expr // nil = len(Source)
	// SourceIsSlice is set by the checker when Source is itself a
	// slice (vs. an owned Array). Sub-slicing has to dereference
	// the parent slice's data_ptr instead of stepping past an
	// owned array's length prefix.
	SourceIsSlice bool
	// IsString is set by the checker when Source is a string. The
	// IR lowers string slicing to a copy-into-fresh-string helper
	// (`__str_slice`) rather than the array-style view shape — a
	// string value is owned + length-prefixed, no separate
	// data-pointer indirection.
	IsString bool
	// ElemType is set by the checker once the source's element
	// type is known. The IR uses it to pick the stride for the
	// `low * stride` byte offset on slice creation. Unused for
	// IsString slices.
	ElemType Type
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
	// TypeArgs is filled by the checker when the callee resolves
	// to a generic function (FuncDecl with non-empty TypeParams).
	// Each entry is the inferred concrete type for the
	// corresponding type parameter, in declaration order. Empty
	// for non-generic calls. The monomorphisation pass uses it
	// to pick the right cloned function and rewrite the callee
	// name to the mangled form.
	TypeArgs []Type
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
	// FloatWidth is set by the checker for float binary ops: 32 for
	// f32 (the default), 64 for f64. Codegen uses it to pick
	// f32.* vs f64.* instructions.
	FloatWidth int
	// IntWidth is set by the checker for integer binary ops: 32 for
	// i32 (the default), 64 for i64. Sub-i32 widths fold through
	// i32 ops for arithmetic; the integer SIZE that matters
	// at the wasm-op level is captured here. Codegen uses it to
	// pick i32.* vs i64.* instructions.
	IntWidth int
	// IsUnsigned is set by the checker when both operands of an
	// integer binary op are unsigned (u32 / u64 / etc.). Codegen
	// uses it to pick the `_u` variant of div / rem / shr /
	// comparison operators.
	IsUnsigned bool
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

// TryKind selects which failure-variant the postfix `?` operator
// short-circuits on. Option's None and Result's Err have
// different shapes (no payload vs E payload) and different
// lowerings, so the checker stamps the kind once it knows the
// source type.
type TryKind int

const (
	TryKindOption TryKind = iota // source is Option[T], failure variant None
	TryKindResult                // source is Result[T, E], failure variant Err(e)
)

// TryOp is the postfix `?` operator: `expr?` evaluates to the
// success payload and early-returns the failure variant when
// the source was failed.
//
//   - Option[T]?  yields T; on None, returns None from the
//     enclosing function (whose return type must be Option[_]).
//   - Result[T, E]? yields T; on Err(e), returns the same Err
//     value through the enclosing function (whose return type
//     must be Result[_, E] — the E must match the source's E).
//
// The checker validates both constraints and fills Kind + Type;
// the IR picks the lowering off Kind. Result's Err lowering
// reuses the source heap object (its tag is already 1, its
// payload at +4 is already the right E) so the early-return is
// a single pointer move with no reallocation.
type TryOp struct {
	P     Position
	Inner Expr
	// Kind is set by the checker once the source type is known.
	Kind TryKind
	// Type is the unwrapped success payload type (Some(T) → T;
	// Ok(T) → T). Lets the IR pick `OpLoad` vs `OpFLoad` for
	// the success-path payload load.
	Type Type
}

// IfExpr is `if (cond) { then_expr } else { else_expr }` in
// expression position. Each arm is exactly one expression (not a
// block of statements) — the construct fills the niche the
// ternary `cond ? then : else` used to occupy, while freeing up
// `?` for the postfix Option-try operator. Statement-form `if
// (cond) { stmts; }` lives on *If and is unrelated.
type IfExpr struct {
	P    Position
	Cond Expr
	Then Expr
	Else Expr
	// IsFloat is set by the checker when the unified arm type is
	// `f32` so the wasm backend picks `if (result f32)`.
	IsFloat bool
}

// StructLit constructs a struct value: `Foo { x: 1, y: 2 }`.
// Fields may appear in any order; the checker reorders them to match
// the declaration so codegen can use fixed offsets.
type StructLit struct {
	P        Position
	TypeName string
	Fields   []FieldInit
	// TypeArgs is filled by the checker when the literal
	// instantiates a generic struct (StructDecl with non-empty
	// TypeParams). Each entry is the inferred concrete type for
	// the corresponding type parameter, in declaration order.
	// The monomorphisation pass uses it to mangle TypeName into
	// `<base>__<arg1>__<arg2>` and clear the field. After
	// monomorph runs, every StructLit has TypeArgs empty.
	TypeArgs []Type
}

// TupleLit is `(e1, e2, …)`. Codegen lowers tuples to heap-allocated
// records — same shape as a struct, but anonymous and addressed by
// position rather than name.
type TupleLit struct {
	P     Position
	Elems []Expr
}

// MapLit is `Map { k: v, k2: v2, ... }`. Distinct from StructLit
// because the keys are arbitrary expressions, not field names.
// Lowers to a `map_new(len(entries))` followed by per-entry
// `m.set(k, v)` calls. The checker fills in `KeyType` /
// `ValueType` from the entries (and reconciles them against the
// destination's `Map[K, V]` Args when one is present); the IR
// uses `KeyType` to inject the runtime `keyKind` tag.
type MapLit struct {
	P         Position
	Entries   []MapEntry
	KeyType   Type
	ValueType Type
}

// MapEntry is a single `key: value` pair inside a MapLit.
type MapEntry struct {
	Key   Expr
	Value Expr
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
func (e *FString) Pos() Position   { return e.P }
func (e *FloatLit) Pos() Position  { return e.P }
func (e *Ident) Pos() Position     { return e.P }
func (e *ArrayLit) Pos() Position  { return e.P }
func (e *Index) Pos() Position     { return e.P }
func (e *SliceExpr) Pos() Position { return e.P }
func (e *Call) Pos() Position      { return e.P }
func (e *Binary) Pos() Position    { return e.P }
func (e *Unary) Pos() Position     { return e.P }
func (e *Assign) Pos() Position      { return e.P }
func (e *IfExpr) Pos() Position      { return e.P }
func (e *MatchExpr) Pos() Position   { return e.P }
func (e *TryOp) Pos() Position       { return e.P }
func (e *StructLit) Pos() Position   { return e.P }
func (e *TupleLit) Pos() Position    { return e.P }
func (e *MapLit) Pos() Position      { return e.P }
func (e *FieldAccess) Pos() Position { return e.P }
func (e *EnumLit) Pos() Position     { return e.P }
func (e *CaptureRef) Pos() Position  { return e.P }
func (e *MakeClosure) Pos() Position { return e.P }

func (*NumberLit) isExpr() {}
func (*CastExpr) isExpr()  {}
func (*BoolLit) isExpr()   {}
func (*StringLit) isExpr() {}
func (*FString) isExpr()   {}
func (*FloatLit) isExpr()  {}
func (*Ident) isExpr()     {}
func (*ArrayLit) isExpr()  {}
func (*Index) isExpr()     {}
func (*SliceExpr) isExpr() {}
func (*Call) isExpr()      {}
func (*Binary) isExpr()    {}
func (*Unary) isExpr()     {}
func (*Assign) isExpr()      {}
func (*IfExpr) isExpr()      {}
func (*MatchExpr) isExpr()   {}
func (*TryOp) isExpr()       {}
func (*StructLit) isExpr()   {}
func (*TupleLit) isExpr()    {}
func (*MapLit) isExpr()      {}
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

// IfLet is `if let <Variant>(b1, b2, …) = <expr> { … }
// [else { … }]` — pattern-binding inside an if without the
// match ceremony. The pattern is a single variant constructor;
// on match, payload fields bind into Bindings (typed via
// BindingTypes, filled by the checker like match arms) and Then
// runs. On mismatch, Else runs (or the if falls through).
//
// Lowered to a one-arm match plus a wildcard fallthrough — see
// the IR pass.
type IfLet struct {
	P            Position
	VariantName  string
	Bindings     []string
	BindingTypes []Type // resolved by the checker; same length as Bindings
	Source       Expr
	Then         Stmt
	Else         Stmt // may be nil
}

// LetElse is `let <Variant>(b1, b2, …) = <expr> else { <divergent>
// };` — pattern-binding declaration with a mandatory-divergent
// else. On match, the bindings are introduced into the enclosing
// scope (live for the rest of the block). On mismatch, the else
// block runs and must terminate the surrounding control flow
// (return / break / continue) — the checker enforces this so
// fall-through into "bindings used uninitialised" is impossible.
type LetElse struct {
	P            Position
	VariantName  string
	Bindings     []string
	BindingTypes []Type
	Source       Expr
	Else         *Block
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

// Defer schedules `Expr` to be evaluated when the enclosing
// function exits (every return path + falloff). Multiple
// defers run in LIFO order. Each Defer node has a synthesised
// `IsActive` local stamped on it by the IR builder; reaching
// the defer statement at runtime sets the local to 1, and the
// per-exit cleanup block only runs the deferred expression
// when the local is set. That makes a defer reached inside a
// conditional a no-op when the conditional didn't fire.
type Defer struct {
	P    Position
	Expr Expr
}

// Arena is `arena { … }` — a syntactic scope whose
// allocations are reclaimed when the block exits. Lowers to
// `arena_save → body → arena_restore` so the bump-allocator
// cursor snaps back to its pre-block value. Anything
// allocated inside the block must NOT escape; the language
// doesn't (yet) statically enforce this — caller's
// responsibility for now.
type Arena struct {
	P    Position
	Body *Block
}
type Var struct {
	P    Position
	Name string
	Type Type // may be nil — inferred
	Init Expr
}

// Destructure is `let (a, b, ...) = expr;` — bind each name to the
// corresponding element of the tuple-typed expression. The checker
// validates Init is a tuple of arity len(Names) and registers a
// synthetic local under TempName so the IR can keep the tuple
// pointer in a slot for the per-name field loads.
type Destructure struct {
	P        Position
	Names    []string
	Init     Expr
	TempName string // checker-stamped: name of the synthesised tuple-holding local.
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
//
// Guard is an optional expression of type bool that's evaluated
// after the pattern matches and the bindings are in scope. When
// the guard is false the arm is skipped — the match falls
// through to the next arm. Spelled `<pattern> when <expr> => …`
// in source. Nil for unconditional arms.
type MatchArm struct {
	P            Position
	VariantName  string   // empty when IsWildcard is true
	Bindings     []string // payload binding names, in payload order
	BindingTypes []Type   // resolved by the checker; same length as Bindings
	IsWildcard   bool     // `_ => …`
	Guard        Expr     // optional `when <expr>`; nil for unconditional arms
	Body         *Block
}

// MatchExpr is `match (e) { Variant(b1, …) => EXPR, _ => EXPR }`
// in expression position. Each arm body is a single expression
// (no statement block, no semicolon) and the whole match
// evaluates to the unified arm type. Mirrors the MatchExpr → IfExpr
// relationship: same parsing/checking shape as the statement-form
// Match, but the body of each arm is an Expr and the construct
// produces a value.
//
// Same exhaustiveness, binding, and guard rules as Match. Reuses
// MatchArm's payload-binding metadata; only Body differs.
type MatchExpr struct {
	P    Position
	Tag  Expr
	Arms []*MatchExprArm
	// IsFloat is set by the checker when the unified arm type is
	// `f32` so the wasm backend picks `block (result f32)`.
	IsFloat bool
}

// MatchExprArm is the expression-form arm. Body is an Expr; all
// other fields mirror MatchArm exactly.
type MatchExprArm struct {
	P            Position
	VariantName  string
	Bindings     []string
	BindingTypes []Type
	IsWildcard   bool
	Guard        Expr
	Body         Expr
}

func (s *Block) Pos() Position    { return s.P }
func (s *If) Pos() Position       { return s.P }
func (s *IfLet) Pos() Position    { return s.P }
func (s *LetElse) Pos() Position  { return s.P }
func (s *While) Pos() Position    { return s.P }
func (s *For) Pos() Position      { return s.P }
func (s *Break) Pos() Position    { return s.P }
func (s *Continue) Pos() Position { return s.P }
func (s *Return) Pos() Position   { return s.P }
func (s *Defer) Pos() Position    { return s.P }
func (s *Arena) Pos() Position    { return s.P }
func (s *Var) Pos() Position      { return s.P }
func (s *Destructure) Pos() Position { return s.P }
func (s *ExprStmt) Pos() Position { return s.P }
func (s *Switch) Pos() Position   { return s.P }
func (s *Match) Pos() Position    { return s.P }
func (s *FuncDecl) Pos() Position { return s.P }

func (*Block) isStmt()    {}
func (*If) isStmt()       {}
func (*IfLet) isStmt()    {}
func (*LetElse) isStmt()  {}
func (*While) isStmt()    {}
func (*For) isStmt()      {}
func (*Break) isStmt()    {}
func (*Continue) isStmt() {}
func (*Return) isStmt()   {}
func (*Defer) isStmt()    {}
func (*Arena) isStmt()    {}
func (*Var) isStmt()      {}
func (*Destructure) isStmt() {}
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
	// TypeParams names the type variables a generic function
	// introduces — `function id[T](x: T): T` declares
	// TypeParams=["T"]. The checker rewrites occurrences of
	// these names in Params / ReturnType to ast.ParamType, then
	// the monomorphisation pass clones the decl per-instantiation
	// before IR lowering. After monomorphisation runs, every
	// FuncDecl that survives has TypeParams empty.
	TypeParams []string
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
	// IsSynthesisedHandlerMain marks the auto-`main()` the
	// checker emits for handler-shaped programs (a top-level
	// `handle(req: HttpRequest): HttpResponse` with no
	// user-defined main). The body is `return tcp_serve(
	// __port_from_env("PORT", 8080), handle);` — exactly what
	// arm64 / wasm-CLI need for a CLI HTTP server. The wasi-
	// http codegen path uses the existing `wasi:http/incoming
	// -handler.handle` export wrapper instead, so it drops the
	// synthesised main (and the tcp_serve transitive imports)
	// before tree-shake runs.
	IsSynthesisedHandlerMain bool
	// Captures is filled by the checker for IsLocal functions: each
	// entry names an outer-scope variable that the body reads, with
	// the variable's static type. The closure-conversion pass uses
	// this list to size the env block and to know how to materialise
	// each capture at the def site.
	Captures []Param
	// UseInferSource is set by the parser on a synthesised
	// `use`-callback FuncDecl when the source omitted the type
	// annotation (`use n <- foo(x);`). It points at the call the
	// callback is being passed into; the checker reads the
	// callee's signature to infer the missing parameter type.
	// Nil otherwise.
	UseInferSource *Call
	// IsPrelude is true for declarations sourced from the
	// auto-injected lang prelude (internal/prelude/prelude.lang).
	// The flag is set at injection time so tests / dump tools
	// can filter prelude noise out of "user code" listings; it
	// has no semantic effect on type-checking, IR, or codegen.
	IsPrelude bool
	// SourceModule is the canonical module path that declared this
	// function. modload stamps every FuncDecl as it loads each
	// module — disk paths get their absolute path; stdlib paths
	// get the `stdlib://…` form modload uses internally. The
	// checker reads this to scope method dispatch to the call
	// site's import closure (module-scoped methods per
	// docs/PRELUDE-TO-MODULES.md). Single-file programs and
	// prelude-injected decls leave this empty.
	SourceModule string
}

// StructDecl is a top-level `struct` declaration. Fields are stored in
// declaration order, which is also the layout order in memory: each
// field occupies 4 bytes and lives at offset 4*index from the struct's
// base pointer.
type StructDecl struct {
	P    Position
	Name string
	// TypeParams names the type variables a generic struct
	// introduces — `struct Pair[A, B] { … }` declares
	// TypeParams=["A", "B"]. The checker rewrites occurrences of
	// these names in field types to ast.ParamType, then the
	// monomorphisation pass clones the decl per-instantiation
	// before IR lowering. After monomorph runs, every StructDecl
	// has TypeParams empty.
	TypeParams []string
	Fields     []Param
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

// UnionDecl is a top-level `type Expr = Binary | Unary | Call;`
// — a closed sum over named struct types. Each member must be
// the name of an existing StructDecl; the checker desugars the
// union into a synthetic EnumDecl whose variants are
// `Name(Name)` (one positional payload per member, of the
// matching struct type) and registers it in `info.Enums`
// alongside hand-written enums. The rest of the pipeline
// (monomorph / IR / codegen) only ever sees the synthetic
// enum.
//
// The sugar buys two ergonomic wins over the hand-written
// equivalent:
//
//   - Member names don't have to be repeated:
//     `type Expr = Binary | Unary | Call` instead of
//     `enum Expr { Binary(Binary), Unary(Unary), Call(Call) }`.
//   - A bare struct literal flows into the union without an
//     explicit wrap: `var e: Expr = Binary{...}` instead of
//     `var e: Expr = Binary(Binary{...})`. The checker's
//     `assignable` rule recognises the (struct, union) pair
//     and inserts the wrapping at the AST level.
//
// Generics aren't supported on unions in the first cut —
// `type Tree[T] = Leaf[T] | Node[T]` would need the desugar
// to thread TypeParams + payload substitution, which adds a
// pass-ordering wrinkle we punt on until self-host needs it.
type UnionDecl struct {
	P    Position
	Name string
	// Members lists the struct names that make up the union, in
	// declaration order. Each name must resolve to a non-generic
	// StructDecl at desugar time. The parser preserves source
	// order; checker rewrites preserve it too so the synthesised
	// enum's variant tags are stable across re-checks.
	Members []string
	// Public marks the union as exported across modules — same
	// semantics as EnumDecl.Public.
	Public bool
}

type Program struct {
	Funcs   []*FuncDecl
	Structs []*StructDecl
	// Enums lists top-level `enum` declarations in source order.
	// Variant constructors look like calls in the parse tree
	// (`Some(x)`); the checker rewrites them to *EnumLit once
	// the variant is resolved.
	Enums []*EnumDecl
	// Unions lists every top-level `type X = A | B | C;` declaration
	// in source order. The checker rewrites each entry into a
	// synthesised EnumDecl appended to Enums, then nils this slice
	// — so monomorph / IR / codegen never see UnionDecl. See
	// UnionDecl's doc comment for the desugaring shape.
	Unions []*UnionDecl
	// Consts lists top-level `const` declarations in source order.
	// The constfold pass evaluates each initialiser, substitutes
	// references throughout the program with the resolved literal,
	// and clears this slice — so the checker / IR lowering / codegen
	// pipeline never sees a ConstDecl.
	//
	// (`state` syntax has been removed; the field that used to
	// carry StateDecl is gone.)
	Consts []*ConstDecl
	// Imports lists every top-level `import "<path>";` declaration
	// in source order. The driver loads the referenced files,
	// mangles their decls under each module's local name, and
	// stitches the combined program before the checker runs.
	// Single-file programs leave this empty.
	Imports []*Import
	// ModuleImports records each loaded module's transitive import
	// closure. The map is keyed by module path; each entry is the
	// set of module paths reachable via `import` chains starting
	// from that module, including the module itself (so a method
	// lookup for "is `<receiver-module>` visible from `<call-site-
	// module>`" is a single map lookup). modload populates the
	// full structure during loading; the checker uses it to scope
	// method dispatch under module-scoped semantics (see
	// docs/PRELUDE-TO-MODULES.md).
	ModuleImports map[string]map[string]bool
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
