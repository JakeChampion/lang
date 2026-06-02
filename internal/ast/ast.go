// Package ast defines the abstract syntax tree for the lang language.
//
// AST kinds are split into three sealed interfaces — Expr, Stmt and Type —
// each with an unexported tag method so foreign packages can match on them
// with a type switch but cannot add new variants.
package ast

import (
	"fmt"
	"sync"
)

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
// `fern -fmt` round-trips the user's chosen spelling instead of
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
// 64; the non-polymorphic zero value is f32 (the parser spells
// `f32` as `FloatType{Width:0, Spelling:"f32"}`, so Width=0 must
// keep meaning f32 there). Both widths are wired through every
// backend. An unsettled float literal carries Polymorphic=true,
// for which NormalWidth defaults to f64 — see NormalWidth.
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

func (NumberType) isType() {}
func (BoolType) isType()   {}
func (VoidType) isType()   {}
func (StringType) isType() {}
func (FloatType) isType()  {}
func (ArrayType) isType()  {}
func (SliceType) isType()  {}
func (TupleType) isType()  {}
func (*FuncType) isType()  {}
func (StructType) isType() {}
func (EnumType) isType()   {}
func (ParamType) isType()  {}
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
func (a ArrayType) String() string {
	if a.Elem == nil {
		return "[]"
	}
	return a.Elem.String() + "[]"
}
func (s SliceType) String() string {
	if s.Elem == nil {
		return "[]"
	}
	return "[" + s.Elem.String() + "]"
}
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
// An unsettled float literal (Polymorphic, Width=0) defaults to
// f64 — the double-precision default every mainstream language
// uses for untyped float literals, and the language's primary
// float type. Defaulting these to f32 silently halved the
// precision of any literal not explicitly annotated `f64` (e.g.
// `var x = 1.0 / 3.0` or a bare `(3.14159).to_string()` receiver).
// An explicit `f32` is spelled `FloatType{Width:0, Spelling:"f32"}`
// by the parser (the historical zero-value-is-f32 convention), so a
// NON-polymorphic Width=0 still maps to 32 — only the Polymorphic
// flag promotes the default to f64.
func (f FloatType) NormalWidth() int {
	if f.Width == 0 {
		if f.Polymorphic {
			return 64
		}
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
// Set to true before `ir.LowerWith` runs; reset after.
//
// Concurrent codegen — e.g. `TestDifferential_LangsmithMain`'s
// per-seed parallelism — must serialise its arm64 + x86_64
// emit calls via `CodegenMu` below. Reads from this flag
// during a backend's emit body are NOT lock-protected; the
// design assumes only one toggling backend runs at a time,
// which `CodegenMu` enforces.
var TwoWordOverride bool

// CaptureNeeds8 reports whether a closure capture of type t must be
// 8-byte aligned in the env block. Only a two-word string on a
// native (ptrW=8) target qualifies: its (data, len) pair is loaded
// with arm64's `ldp`, which faults on an unaligned address. Every
// other capture — i64 / f64 / pointers / single-word strings — uses
// a plain ldr/str that tolerates any offset, so they stay packed
// (keeping the layout, and every existing offset test, unchanged).
func CaptureNeeds8(t Type, ptrW int) bool {
	_, isStr := t.(StringType)
	return isStr && UseTwoWordStrings(ptrW) && ptrW == 8
}

// CaptureAlign rounds a running env-block offset up to the alignment
// the next capture of type t requires (8 for pointer/wide/two-word
// captures, 4 otherwise). Every closure-env layout — the canonical
// one in closureconv plus each backend's store loop — must apply
// this identically so a backend's store offsets match the
// CaptureRef load offsets. Without it, an i32 capture before a
// string left the string at a 4-aligned offset and arm64 segfaulted
// on the unaligned two-word load.
func CaptureAlign(off int32, t Type, ptrW int) int32 {
	if !CaptureNeeds8(t, ptrW) {
		return off
	}
	return (off + 7) &^ 7
}

// RcFreeEnabled gates the Phase 3 freelist allocator. When true (the
// DEFAULT, as of step 5), codegen emits a segregated freelist:
// `__fern_free` returns a block to its size class and `__fern_alloc`
// reuses a class's freelist before bumping, and the array dec sites
// (__fern_drop_arr_ptr / __fern_arr_dec) return OWNED array buffers
// to it at rc==0. When false, `__fern_alloc` is a pure bump cursor
// and `__fern_free` is a no-op — the pre-step-5 leak-forever arena.
//
// FLIPPED ON. Both over-release classes are closed by the borrow-aware
// analysis (computeFreeEligible):
//   - borrowed-IN: excludes params + anything derived from them.
//   - ESCAPE-OUT: a local that escapes into a container retained
//     WITHOUT an inc — `map.set` / MapLit values, pushed array
//     elements, enum-constructor payloads, index / field / capture
//     assignment targets — is tainted so the owner never frees out
//     from under the container. (StructLit / TupleLit construction inc
//     their stored values, so escaping through those is already safe.)
//
// rc_correctness's escape_array_into_* entries cover each sink free-on
// on all three backends; the differential gate
// (Test{X86_64,Arm64,WASM}FixturesFreeMatchesNoFree) asserts free-on ==
// free-off byte-for-byte. The flip landed after that corpus + gate went
// green corpus-wide on all backends in CI plus owner sign-off. A
// handful of tests pin the OFF arena via save/restore for differential
// baselines. What LEAKS (safe — no over-release): borrowed /
// borrowed-derived buffers, struct / enum / closure / map boxes,
// struct/enum array fields.
var RcFreeEnabled = true

// RcFreeDebug turns the freelist into a use-after-free DETECTOR
// (x86_64 only; a diagnostic build mode, set alongside
// RcFreeEnabled). Instead of recycling a freed array buffer, the
// free sites poison its rc word with RcPoison and quarantine the
// block (never handed back — __fern_alloc keeps bumping).
// __fern_rc_inc / __fern_rc_dec then trap (ud2 → SIGILL) the moment
// they touch a poisoned block — i.e. a stale reference to an
// over-released buffer — so a gdb backtrace pinpoints the holder the
// rc undercounted. Chases the residual array over-release that
// blocks the step-5 flip (see RC-PERCEUS-PLAN.md).
var RcFreeDebug = false

// RcPoison is the rc-word marker a quarantined (freed) block carries
// in RcFreeDebug mode: a large positive value that can't be a real
// refcount and isn't the high-bit static sentinel.
const RcPoison = 0x7EEDFACE

// CodegenMu serialises native codegen calls that read or
// write `TwoWordOverride`. arm64.Emit toggles the flag during
// its Emit body; x86_64.Emit reads it via `ir.LowerWith`.
// Before this mutex existed, parallel arm64 emits could
// stack their toggles such that one goroutine's defer
// restored the flag to `false` while another goroutine was
// still mid-emit — producing single-word `string_from_bytes`
// inside an arm64 program that otherwise expects two-word
// strings. Symptom: SIGSEGV on the first f-string / string
// concat that landed in the same diff-oracle batch as
// another seed.
//
// wasmbin doesn't acquire this lock: it always passes ptrW=4
// to `ir.LowerWith`, and `UseTwoWordStrings(4)` short-
// circuits to `true` without reading `TwoWordOverride`.
var CodegenMu sync.Mutex

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
	// EnumName, when set, identifies the enum this Ident is a
	// variant of. Stamped by the checker after resolving a
	// qualified-variant reference (`Color.Red`) or after picking
	// one of several same-named variants by context. Empty on all
	// other Idents. The IR's `lookupVariant` prefers a non-empty
	// EnumName over its global walk, which is the only thing that
	// keeps variant resolution deterministic when two enums
	// declare the same variant name.
	EnumName string
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
	// Method is set by the checker when this Call was rewritten
	// from a `target.method(args)` source-level method call. The
	// rest of the pipeline ignores it; the LSP reads it to answer
	// hover / definition on the original method name (which
	// otherwise disappears once the call is rewritten to
	// `__method_Type_name(target, args)`).
	Method *MethodCallSite
	// Module is set by modload when this Call was rewritten from a
	// qualified cross-module call (`mod.fn(args)`). Same LSP-only
	// rationale as Method.
	Module *ModuleCallSite
	// IsVariantCall is set by the checker when this Call resolved
	// to a variant constructor (`Some(x)`, `Ok(v)`, `Square(2.0,
	// 3.0)`) rather than a regular function call. Downstream
	// passes consult this to gate variant-specific behaviour;
	// notably `postSettleType`'s Call branch rebuilds an
	// EnumType's Args from the call's arg widths after a
	// `settleNumeric` pass, and that rebuild MUST NOT fire on
	// regular function calls that happen to return an EnumType
	// (without the flag, `f(p: boolean[]): Option[i32]` had its
	// return type rebuilt as `Option[boolean[]]`).
	IsVariantCall bool
}

// MethodCallSite records the original source-level shape of a
// method call before the checker rewrote it. Field is the
// method name as the user wrote it, FieldPos points at that
// name's position in the source, and Receiver is the resolved
// owner type (e.g. ast.StructType{Name:"Point"}). The LSP locates
// the call via FieldPos and uses Receiver to look up the
// implementation in Info.Methods.
type MethodCallSite struct {
	Field    string
	FieldPos Position
	Receiver Type
}

// ModuleCallSite is the cross-module analogue of MethodCallSite:
// modload rewrites `mod.fn(args)` to a flat `mangled_fn(args)`
// Ident and the field-name position would otherwise be lost. The
// LSP uses FieldPos to locate hover / goto-def on the unqualified
// function name; Mangled is the modload-rewritten target name so
// the dispatcher can look up the FuncDecl directly.
type ModuleCallSite struct {
	Module    string   // local module name as the user wrote it (e.g. "util")
	ModulePos Position // start of the module qualifier
	Field     string   // unqualified function / const name (e.g. "foo")
	FieldPos  Position // start of the field after the `.`
	Mangled   string   // modload's flat name (e.g. "util__foo")
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
	// Base is the spread source of a struct-update literal
	// `Foo { ...base, field: v }`: nil for a plain literal (which
	// must name every field), non-nil when the literal copies the
	// un-named fields from `base` and overrides only the listed
	// ones. `base` must have the same struct type as TypeName. See
	// docs/IMMUTABILITY-MIGRATION-PLAN.md.
	Base Expr
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
	Name string
	// NamePos is the position of the Name token in the source (the
	// `x` in `Point { x: 1, y: 2 }`). The parser populates it for
	// every literal it parses; synthetic FieldInits inserted by
	// downstream passes leave it zero. The LSP uses this for
	// rename of struct fields — without it, occurrences inside
	// struct literals would be unrewrittable.
	NamePos Position
	Value   Expr
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
//
// P is the position of the `.` token (kept for backwards-compat with
// pre-LSP error sites that point at the access expression). FieldPos
// is the position of the field-name identifier just past the dot —
// what the LSP uses for hover / definition queries on `p.x` so the
// cursor on `x` resolves rather than the cursor on the dot.
// FieldPos is zero (Line=0) on synthetic FieldAccesses inserted by
// downstream passes (e.g. method-call rewriting) that don't have a
// real source location.
type FieldAccess struct {
	P        Position
	Target   Expr
	Field    string
	FieldPos Position
}

// Lambda is an anonymous function expression: `function (x: i32):
// i32 { return x; }`. It's the expression-position counterpart to
// the FuncDecl statement form — same params / return type / body
// shape, no Name. The checker treats it like a local FuncDecl:
// runs capture analysis against the enclosing scope and fills
// `Captures` with the names this lambda reads from outer-scope.
// The closureconv pass synthesises a hoisted top-level FuncDecl
// from the Lambda (with a fresh `__lambda_<N>` name) and replaces
// the Lambda expression with a MakeClosure at its source location
// — same end-shape as a named local FuncDecl declared as a stmt.
type Lambda struct {
	P          Position
	Params     []Param
	ReturnType Type
	Body       *Block
	// Captures gets filled by the checker, same shape as
	// FuncDecl.Captures. closureconv reads it to size the env
	// block.
	Captures []Param
	// Synthetic is the throwaway FuncDecl the checker swaps in
	// as `c.current` while walking this lambda's body. Var
	// statements inside the body append themselves to
	// `info.Locals[Synthetic]`; closureconv re-keys those locals
	// onto the hoisted FuncDecl it produces. Nil until the
	// checker visits this node.
	Synthetic *FuncDecl
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

func (e *NumberLit) Pos() Position   { return e.P }
func (e *CastExpr) Pos() Position    { return e.P }
func (e *BoolLit) Pos() Position     { return e.P }
func (e *StringLit) Pos() Position   { return e.P }
func (e *FString) Pos() Position     { return e.P }
func (e *FloatLit) Pos() Position    { return e.P }
func (e *Ident) Pos() Position       { return e.P }
func (e *ArrayLit) Pos() Position    { return e.P }
func (e *Index) Pos() Position       { return e.P }
func (e *SliceExpr) Pos() Position   { return e.P }
func (e *Call) Pos() Position        { return e.P }
func (e *Binary) Pos() Position      { return e.P }
func (e *Unary) Pos() Position       { return e.P }
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
func (e *Lambda) Pos() Position      { return e.P }

func (*NumberLit) isExpr()   {}
func (*CastExpr) isExpr()    {}
func (*BoolLit) isExpr()     {}
func (*StringLit) isExpr()   {}
func (*FString) isExpr()     {}
func (*FloatLit) isExpr()    {}
func (*Ident) isExpr()       {}
func (*ArrayLit) isExpr()    {}
func (*Index) isExpr()       {}
func (*SliceExpr) isExpr()   {}
func (*Call) isExpr()        {}
func (*Binary) isExpr()      {}
func (*Unary) isExpr()       {}
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
func (*Lambda) isExpr()      {}

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

type Var struct {
	P    Position
	Name string
	Type Type // may be nil at parse time — inferred + stamped by the checker
	Init Expr
	// WasAnnotated records whether the source carried a `: Type`
	// annotation after the name. Set by the parser before checker
	// stamps an inferred type into Type, so the LSP's inlay-hint
	// pass can tell "user wrote it" from "checker filled it in".
	WasAnnotated bool
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
//
// Literal, when non-nil, marks this arm as a literal-pattern
// arm (`0 => …`, `"yes" => …`, `true => …`). Mutually exclusive
// with VariantName / IsWildcard — the parser sets exactly one
// of {Literal, IsWildcard, VariantName}. Literal-pattern arms
// dispatch via equality comparison instead of tag-based match.
type MatchArm struct {
	P           Position
	VariantName string // empty when IsWildcard or Literal != nil
	// VariantModule is the optional `mod.` qualifier on a variant
	// pattern (`mod.TokA(x) => …`). Set by the parser when the
	// pattern spells the module name; empty for unqualified
	// patterns. The checker validates it matches the scrutinee
	// enum's source module when both are known.
	VariantModule string
	Bindings      []string // payload binding names, in payload order
	BindingTypes  []Type   // resolved by the checker; same length as Bindings
	IsWildcard    bool     // `_ => …`
	Literal       Expr     // `0 => …` / `"yes" => …` / `true => …`; nil otherwise
	Guard         Expr     // optional `when <expr>`; nil for unconditional arms
	Body          *Block
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
// other fields mirror MatchArm exactly — including the optional
// Literal field for literal-pattern arms (`0 => …`, `"yes" => …`).
type MatchExprArm struct {
	P             Position
	VariantName   string
	VariantModule string // optional `mod.` qualifier — same semantics as MatchArm.VariantModule
	Bindings      []string
	BindingTypes  []Type
	IsWildcard    bool
	Literal       Expr // literal pattern; mutually exclusive with VariantName / IsWildcard
	Guard         Expr
	Body          Expr
}

func (s *Block) Pos() Position                  { return s.P }
func (s *If) Pos() Position                     { return s.P }
func (s *IfLet) Pos() Position                  { return s.P }
func (s *LetElse) Pos() Position                { return s.P }
func (s *While) Pos() Position                  { return s.P }
func (s *For) Pos() Position                    { return s.P }
func (s *Break) Pos() Position                  { return s.P }
func (s *Continue) Pos() Position               { return s.P }
func (s *Return) Pos() Position                 { return s.P }
func (s *Defer) Pos() Position                  { return s.P }
func (s *Var) Pos() Position                    { return s.P }
func (s *Destructure) Pos() Position            { return s.P }
func (s *ExprStmt) Pos() Position               { return s.P }
func (s *Switch) Pos() Position                 { return s.P }
func (s *Match) Pos() Position                  { return s.P }
func (s *FuncDecl) Pos() Position               { return s.P }
func (s *FuncDecl) GenericName() string         { return s.Name }
func (s *FuncDecl) GenericTypeParams() []string { return s.TypeParams }

func (*Block) isStmt()       {}
func (*If) isStmt()          {}
func (*IfLet) isStmt()       {}
func (*LetElse) isStmt()     {}
func (*While) isStmt()       {}
func (*For) isStmt()         {}
func (*Break) isStmt()       {}
func (*Continue) isStmt()    {}
func (*Return) isStmt()      {}
func (*Defer) isStmt()       {}
func (*Var) isStmt()         {}
func (*Destructure) isStmt() {}
func (*ExprStmt) isStmt()    {}
func (*Switch) isStmt()      {}
func (*Match) isStmt()       {}
func (*FuncDecl) isStmt()    {} // legal as a stmt only when IsLocal is true

// ---------- Top level ----------

type Param struct {
	Name string
	// NamePos is the position of the Name token in the source (the
	// `a` in `function add(a: i32, …)` or the `x` in a struct
	// declaration `struct Point { x: i32, … }`). Synthetic Params
	// (the receiver injected by closure conversion, generic-call
	// rewrites) leave it zero. The LSP uses this for parameter
	// + struct-field rename + hover; without it occurrences in
	// declarations would be unrewrittable.
	NamePos Position
	Type    Type
}

type FuncDecl struct {
	P    Position
	Name string
	// NamePos is the position of the Name token after the
	// `function` keyword + any receiver clause. Synthetic decls
	// (the auto-generated `main` for handler-shaped programs,
	// closure-converted hoists, monomorphisation clones) leave it
	// zero. Used by the LSP to rewrite both the decl site and
	// every call-site reference during rename.
	NamePos Position
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
// GenericDecl is the common shape of a top-level declaration that
// the monomorphisation pass treats uniformly: it has a name, a
// list of type-parameter names, and a position. Both *FuncDecl
// and *StructDecl satisfy it, which lets monomorph drive the
// "is this name generic?" check + "drop the generic decls" pass
// against a single map instead of running parallel paths over
// `info.GenericFuncs` and `info.GenericStructs`. The clone +
// substitute logic stays per-kind (function bodies vs struct
// fields are genuinely different work) and dispatches off the
// concrete type.
type GenericDecl interface {
	Node
	GenericName() string
	GenericTypeParams() []string
}

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
	// SourceModule mirrors FuncDecl.SourceModule — modload stamps
	// the canonical module path that declared this struct so the
	// LSP can answer cross-module goto-definition queries (jump
	// from `util.Point` use site to `Point`'s declaration in
	// util.fern). Empty for parser-only single-file programs and
	// prelude-injected decls.
	SourceModule string
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
	// SourceModule mirrors FuncDecl.SourceModule. See StructDecl
	// for the cross-module-LSP rationale.
	SourceModule string
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
	// SourceModule is the canonical module path that declared this
	// union. modload stamps it during loadRecursive; the checker
	// propagates it to the synthesised EnumDecl so cross-module
	// variant-pattern qualifier checks have a comparable target.
	SourceModule string
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
	// LoadedStdlibPaths records every `std/…` / `core/…` canonical
	// path modload pulled in (keyed by the `stdlib://…` path form
	// modload uses internally — see `internal/modload/modload.go`).
	// The checker's auto-prelude path consults this set so a
	// prelude file declaring `import "std/foo";` doesn't re-inject
	// `std/foo` when the user's entry program already imported the
	// same module via modload. Transitional plumbing for the
	// prelude-to-modules migration; goes away once auto-prelude
	// injection does (Phase 5 in docs/PRELUDE-TO-MODULES.md).
	LoadedStdlibPaths map[string]bool
	// Comments lists every `//` line comment the lexer collected,
	// in source order. Most consumers (checker, IR lowering,
	// codegen) ignore this field; the formatter walks it alongside
	// the AST to re-emit comments at their original positions.
	Comments []Comment
	// BlankLines lists the 1-based source line numbers that were
	// blank (whitespace-only). Like Comments, only the formatter
	// consumes it — to preserve an author's blank-line grouping
	// inside blocks rather than collapsing every statement together.
	BlankLines []int
	// TypeRefs records every named-type reference the parser saw
	// in a type-annotation slot (`var c: Color`, `Option[T]`,
	// `pub function f(x: Point): Result[i32, Err]`, field type
	// lists, etc.). Each entry is `(position, source-spelling)`
	// for the name token alone — composite parts (`[T]`, `T[]`,
	// `(T, U)`, `() => T`) get their own entries from the recursive
	// parseType descent. The LSP queries this to answer
	// "what type is at this position?" because ast.Type values are
	// positionless; without this table, type-annotation hover
	// (`var c: Color`) and goto-def on type names can't work.
	// Modload prepends per-module-mangled forms during loading;
	// the checker leaves it alone.
	TypeRefs []TypeRef
}

// TypeRef is a parser-recorded source location for one named-type
// reference. Name carries the source spelling exactly as it
// appeared (including any `mod.Foo` qualifier and any generic-args
// suffix is NOT included — the args are separate TypeRef entries).
// Consumers cross-reference Name against checker.Info.Structs /
// .Enums to describe / locate the resolved decl.
type TypeRef struct {
	P    Position
	Name string
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
	P    Position
	Path string
	// LocalName is the qualifier used in `mod.fn(...)` / `mod.Type`
	// references — the alias if one was given, else the path's
	// basename. modload keys its per-module import table off this.
	LocalName string
	// Alias is the explicit `as <name>` binding, or "" when the
	// import used the default basename qualifier. Kept distinct from
	// LocalName so the printer can round-trip the `as` clause.
	Alias string
}

// Pos accessors for top-level declarations that aren't also Stmts.
// FuncDecl already has Pos() via its Stmt role; the rest need their
// own so they satisfy the ast.Node interface for Walk / WalkProgram.
func (d *StructDecl) Pos() Position               { return d.P }
func (d *StructDecl) GenericName() string         { return d.Name }
func (d *StructDecl) GenericTypeParams() []string { return d.TypeParams }
func (d *EnumDecl) Pos() Position                 { return d.P }
func (d *UnionDecl) Pos() Position                { return d.P }
func (d *ConstDecl) Pos() Position                { return d.P }
func (d *Import) Pos() Position                   { return d.P }
