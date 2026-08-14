// Package ast defines the abstract syntax tree for the lang language.
//
// AST kinds are split into three sealed interfaces — Expr, Stmt and Type —
// each with an unexported tag method so foreign packages can match on them
// with a type switch but cannot add new variants.
package ast

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
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

// StrType is the borrowed-string VIEW type `str` (#4813 / #4297 Option A) --
// the string sibling of the ArrayType-vs-SliceType precedent. `string` owns
// its heap box; a `str` is a non-owning view of some string's bytes: it must
// never be freed by its holder. In this first slice no producer yields `str`
// yet -- the type exists so signatures can declare borrowed-string intent: an
// owned `string` freely borrows INTO a `str` (assignable; any argument
// position), while `str` never silently promotes to an owned `string`
// (.to_owned() materialises a fresh copy). `str` shares the `string` method
// surface (methodTypeName maps it to "string"). Runtime shape: identical to
// StringType (the #4294 immortal rc=-1 view box IS the runtime `str`), so it
// is ERASED to StringType at the LowerWith choke point (ir/erase_str.go),
// mirroring HandleType erasure -- no backend or width classification ever
// sees it. The escape/dangling rule is the A2 slice (#4814).
type StrType struct{}

// CharType is `char`: a single Unicode SCALAR VALUE (0..0x10FFFF, excluding
// the surrogate range D800..DFFF) — Rust's `char`, Swift's `Unicode.Scalar`,
// C#'s `Rune`. It exists because a byte and a code point were previously the
// SAME type (`i32`), so `s[i].to_upper()` (an ASCII byte fold) and
// `unicode.to_upper_char(cp)` (a Unicode mapping) had identical signatures and
// neither the checker nor a reader could tell them apart — the root confusion
// behind #5552, and the reason the ASCII/Unicode split can't survive as a
// naming convention across a 144-method surface (docs/STRINGS-SOTA.md D2).
//
// In this first slice NO stdlib producer yields `char` yet — the type exists
// so signatures can declare scalar-vs-byte intent, mirroring how StrType
// landed. Conversion is EXPLICIT in both directions (`c as i32`, `n as char`);
// a `char` never implicitly becomes an integer or vice versa, which is the
// whole point. Range/surrogate validation on `as char` is deferred with the
// producers (#5629 slice 5).
//
// Runtime shape: an i32 slot, identical to NumberType{Width:32}, so it is
// ERASED to i32 at the LowerWith choke point (ir/erase_char.go) exactly as
// StrType erases to StringType — no backend or width classification ever sees
// it.
type CharType struct{}

// NeverType is the bottom type: the type of an expression that never
// yields a value because every control-flow path through it exits
// early (`return` / `break` / `continue`). It arises only internally
// — a value-position block-expression whose statements always diverge
// has no trailing value, so instead of being `void` (which would be a
// type error where a value is required) it is `never`, which is
// assignable to / unifies with any type. It is never written in
// source, so the parser and printer don't produce it; it flows out of
// `checker.checkBlockExpr` into assignability and if/match arm-type
// unification. See docs/BLOCK-EXPRESSIONS.md (#4522).
type NeverType struct{}

// FloatType represents an IEEE-754 binary float. Width is 32 or
// 64; the non-polymorphic zero value is f32 (the parser spells
// `f32` as `FloatType{Width:0, Spelling:"f32"}`, so Width=0 must
// keep meaning f32 there). `float` is the width-unqualified
// alias for f64 (#5363) — the parser spells it
// `FloatType{Width:64, Spelling:"float"}`. Both widths are wired
// through every backend. An unsettled float literal carries
// Polymorphic=true, for which NormalWidth defaults to f64 — see
// NormalWidth.
//
// Spelling matches NumberType.Spelling — captures the type name
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

// StreamType is the WASI Preview-3 async data channel `stream[T]` — a sequence
// of `T` delivered incrementally over the wire. It appears in Fern source only
// as the result type of an async `@import` (e.g. `async function body():
// stream[u8]`); under the colorless model the call site yields the fully
// collected `T[]` (the compiler drives `stream.read` + the await loop to EOF —
// see docs/STREAM-TYPE-SURFACE.md). `future[T]` is intentionally NOT a surface
// type (colorless auto-await subsumes a single deferred value).
type StreamType struct{ Elem Type }

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

// SelfType is the contextual `Self` type that appears inside a trait
// declaration's method signatures and inside `impl Trait for Type`
// bodies. The parser substitutes SelfType with the impl's concrete
// `for` type when it desugars impl methods into ordinary receiver
// methods, so SelfType never reaches the monomorph / IR / codegen
// stages — it survives only on a trait's registered signatures (used
// by the conformance check). See docs/TRAITS.md.
type SelfType struct{}

// ProjType is an associated-type projection `Base::Name` — `Self::Item`
// inside a trait or impl, `T::Item` in a bounded generic, or a concrete
// `Foo::Item`. The checker resolves a concrete-base projection to the
// impl's `type Item = …` binding immediately; a `Self`/`ParamType`-based
// one stays abstract until the impl method is conformance-checked or the
// generic is monomorphised, at which point Base becomes concrete and the
// binding is looked up. See docs/ASSOCIATED-TYPES.md.
type ProjType struct {
	Base Type
	Name string
}

// DynTraitType is a runtime trait-object type, written `dyn Trait` (a
// single trait) or `dyn A + B` (a multi-trait object) in type position.
// A concrete value whose type implements EVERY trait in the set coerces
// to it (the checker's assignability gate); a method call on a
// `dyn …` value resolves the method across the UNION of the traits'
// method sets and dispatches at runtime by the value's concrete type
// rather than being statically rewritten. `dyn` is the open,
// runtime-dispatched counterpart to the static `impl`/bounded-generic
// path — see docs/DYN-TRAITS.md.
//
// Traits is kept SORTED and DEDUPED at construction (see NewDynTraitType)
// so the set is canonical: `dyn A + B` ≡ `dyn B + A`, `Equal` is a plain
// slice compare, and `String()` is deterministic. A single-trait
// `dyn A` is the 1-element case. Use Trait0() for genuinely
// single-trait-only contexts; multi-trait-aware code iterates Traits.
type DynTraitType struct {
	Traits []string
	// Args carries the generic trait-arguments for each trait, parallel
	// to Traits (Args[i] are the arguments for Traits[i]). It is nil for
	// the common non-generic case, and an entry is nil/empty for any
	// individual non-generic trait in a mixed set. `dyn Container[i32]`
	// is Traits=["Container"], Args=[[i32]]; the runtime erases the
	// arguments (the vtable is keyed by trait name), so Args drives only
	// the checker's coercion gate and method-signature substitution.
	Args [][]Type
	// AssocBindings carries the pinned associated-type bindings for each
	// trait, parallel to Traits (AssocBindings[i] are the `Name = Type`
	// pins for Traits[i]). A trait with associated types is object-unsafe
	// UNLESS the `dyn` type pins every one — `dyn Producer[Item = i32]` is
	// Traits=["Producer"], AssocBindings=[[{Item, i32}]]. Like Args, the
	// runtime erases them; they drive only the checker's object-safety
	// gate, coercion gate, and the `Self::Item` projection resolution in
	// method signatures. Each trait's bindings are kept sorted by name at
	// construction so Equal is an elementwise compare.
	AssocBindings [][]AssocBinding
}

// AssocBinding is one pinned associated type in a `dyn` object: `Item = i32`.
type AssocBinding struct {
	Name string
	Type Type
}

// ArgsFor returns the generic trait-arguments for the i-th trait, or nil
// when the trait is non-generic (or Args is short / absent).
func (d DynTraitType) ArgsFor(i int) []Type {
	if i < 0 || i >= len(d.Args) {
		return nil
	}
	return d.Args[i]
}

// AssocFor returns the pinned associated-type bindings for the i-th trait, or
// nil when the trait pins none (or AssocBindings is short / absent).
func (d DynTraitType) AssocFor(i int) []AssocBinding {
	if i < 0 || i >= len(d.AssocBindings) {
		return nil
	}
	return d.AssocBindings[i]
}

// NewDynTraitType builds a DynTraitType from a trait-name set, normalising
// it to the canonical sorted+deduped form. Callers (the parser, modload,
// any code synthesising a `dyn` type) should go through this so the
// invariant holds everywhere. Use NewDynTraitTypeFull for generic traits /
// pinned associated types.
func NewDynTraitType(traits ...string) DynTraitType {
	if len(traits) <= 1 {
		return DynTraitType{Traits: append([]string(nil), traits...)}
	}
	cp := append([]string(nil), traits...)
	sort.Strings(cp)
	out := cp[:0]
	var prev string
	for i, t := range cp {
		if i == 0 || t != prev {
			out = append(out, t)
			prev = t
		}
	}
	return DynTraitType{Traits: out}
}

// NewDynTraitTypeFull builds a DynTraitType carrying per-trait generic
// arguments (args) and pinned associated-type bindings (assoc), both parallel
// to traits, normalising to canonical sorted + deduped form. The (trait, args,
// assoc) triples sort
// together by trait name, with the args' and assoc' string forms breaking
// ties. Each trait's assoc bindings are sorted by name so Equal is an
// elementwise compare. When both args and assoc are all-empty this is
// equivalent to NewDynTraitType.
func NewDynTraitTypeFull(traits []string, args [][]Type, assoc [][]AssocBinding) DynTraitType {
	if len(args) != len(traits) {
		na := make([][]Type, len(traits))
		copy(na, args)
		args = na
	}
	if len(assoc) != len(traits) {
		nb := make([][]AssocBinding, len(traits))
		copy(nb, assoc)
		assoc = nb
	}
	any := false
	for _, a := range args {
		if len(a) > 0 {
			any = true
		}
	}
	for i := range assoc {
		if len(assoc[i]) > 0 {
			// Canonicalise each trait's bindings by name.
			sort.SliceStable(assoc[i], func(x, y int) bool { return assoc[i][x].Name < assoc[i][y].Name })
			any = true
		}
	}
	if !any {
		return NewDynTraitType(traits...)
	}
	idx := make([]int, len(traits))
	for i := range idx {
		idx[i] = i
	}
	key := func(i int) string {
		return traits[i] + "\x00" + typeArgsString(args[i]) + "\x00" + assocString(assoc[i])
	}
	sort.SliceStable(idx, func(a, b int) bool { return key(idx[a]) < key(idx[b]) })
	outT := make([]string, 0, len(traits))
	outA := make([][]Type, 0, len(traits))
	outB := make([][]AssocBinding, 0, len(traits))
	var prev string
	for n, i := range idx {
		k := key(i)
		if n == 0 || k != prev {
			outT = append(outT, traits[i])
			outA = append(outA, args[i])
			outB = append(outB, assoc[i])
			prev = k
		}
	}
	return DynTraitType{Traits: outT, Args: outA, AssocBindings: outB}
}

// assocString renders pinned associated-type bindings as `[Name = T, …]`
// (empty string for none) — used for canonical ordering + String().
func assocString(bs []AssocBinding) string {
	if len(bs) == 0 {
		return ""
	}
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Name + " = " + b.Type.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// typeArgsString renders a generic-argument list as `[a, b]` (empty string
// for none) — used for the canonical ordering of dyn-trait sets.
func typeArgsString(args []Type) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Trait0 returns the first (and, for a single-trait object, only) trait
// name. It is for genuinely single-trait contexts (e.g. the compiled
// backends, which currently only lower single-trait `dyn`). Multi-trait
// aware code must iterate Traits instead. Returns "" for an empty set
// (should never happen for a validly-parsed type).
func (d DynTraitType) Trait0() string {
	if len(d.Traits) == 0 {
		return ""
	}
	return d.Traits[0]
}

// HandleType is a WIT resource-handle type (P5 — docs/WIT-BRING-YOUR-OWN.md),
// written `own R` / `borrow R` in type position where R names a top-level
// `resource` declaration. `own R` is an owned handle (consuming — dropped when
// it goes out of scope, in a later P5 slice); `borrow R` is a non-consuming
// view (never dropped). A bare resource name `R` in type position parses as a
// StructType and the checker reclassifies it to an owned HandleType.
//
// A handle is an opaque i32 at the canonical ABI — NOT pointer-shaped — so it
// sizes and stores like a scalar. Handle type-safety is enforced entirely in
// the checker; the `ir.LowerWith` choke point erases HandleType to plain i32
// (NumberType{}) before any compiled backend, the interpreter, or the
// self-host emitter sees it (see internal/ir/erase_handles.go).
type HandleType struct {
	Resource string
	Borrowed bool
}

func (NumberType) isType()   {}
func (SelfType) isType()     {}
func (BoolType) isType()     {}
func (VoidType) isType()     {}
func (NeverType) isType()    {}
func (StringType) isType()   {}
func (StrType) isType()      {}
func (CharType) isType()     {}
func (FloatType) isType()    {}
func (ArrayType) isType()    {}
func (StreamType) isType()   {}
func (SliceType) isType()    {}
func (TupleType) isType()    {}
func (*FuncType) isType()    {}
func (StructType) isType()   {}
func (EnumType) isType()     {}
func (ParamType) isType()    {}
func (DynTraitType) isType() {}
func (HandleType) isType()   {}
func (ProjType) isType()     {}
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
func (NeverType) String() string  { return "never" }
func (StringType) String() string { return "string" }
func (StrType) String() string    { return "str" }
func (CharType) String() string   { return "char" }
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
func (s StreamType) String() string {
	if s.Elem == nil {
		return "stream"
	}
	return "stream[" + s.Elem.String() + "]"
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
func (SelfType) String() string    { return "Self" }
func (d DynTraitType) String() string {
	parts := make([]string, len(d.Traits))
	for i, t := range d.Traits {
		// Positional generic args and pinned associated-type bindings share
		// one bracket: `Foo[i32]`, `Producer[Item = i32]`, or both.
		var inner []string
		for _, a := range d.ArgsFor(i) {
			inner = append(inner, a.String())
		}
		for _, b := range d.AssocFor(i) {
			inner = append(inner, b.Name+" = "+b.Type.String())
		}
		if len(inner) > 0 {
			parts[i] = t + "[" + strings.Join(inner, ", ") + "]"
		} else {
			parts[i] = t
		}
	}
	return "dyn " + strings.Join(parts, " + ")
}
func (h HandleType) String() string {
	if h.Borrowed {
		return "borrow " + h.Resource
	}
	return "own " + h.Resource
}
func (p ProjType) String() string {
	base := "<nil>"
	if p.Base != nil {
		base = p.Base.String()
	}
	return base + "::" + p.Name
}

// SubstSelf recursively replaces every SelfType in t with self. Used
// when desugaring `impl Trait for Type` methods (the parser) and when
// checking impl conformance against a trait's Self-typed signatures
// (the checker). See docs/TRAITS.md.
func SubstSelf(t Type, self Type) Type {
	switch tt := t.(type) {
	case SelfType:
		return self
	case ArrayType:
		return ArrayType{Elem: SubstSelf(tt.Elem, self)}
	case StreamType:
		return StreamType{Elem: SubstSelf(tt.Elem, self)}
	case SliceType:
		return SliceType{Elem: SubstSelf(tt.Elem, self)}
	case TupleType:
		elems := make([]Type, len(tt.Elems))
		for i, e := range tt.Elems {
			elems[i] = SubstSelf(e, self)
		}
		return TupleType{Elems: elems}
	case StructType:
		if len(tt.Args) == 0 {
			return tt
		}
		args := make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			args[i] = SubstSelf(a, self)
		}
		return StructType{Name: tt.Name, Args: args}
	case EnumType:
		args := make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			args[i] = SubstSelf(a, self)
		}
		return EnumType{Name: tt.Name, Args: args}
	case *FuncType:
		params := make([]Type, len(tt.Params))
		for i, pt := range tt.Params {
			params[i] = SubstSelf(pt, self)
		}
		return &FuncType{Params: params, Result: SubstSelf(tt.Result, self)}
	case ProjType:
		return ProjType{Base: SubstSelf(tt.Base, self), Name: tt.Name}
	default:
		return t
	}
}

// CloneBlock / CloneStmt / CloneExpr deep-copy a statement tree so an
// in-place rewrite of the copy (type substitution, dispatch resolution,
// numeric-literal settling) never leaks into the original. Leaf
// expressions are still pointer-copied so a caller can swap fields
// without aliasing the source node. The checker uses CloneBlock to
// materialise a trait's default method body once per implementing type
// (see docs/TRAITS.md); monomorph uses all three to instantiate a
// generic function's body per type-argument set.
func CloneBlock(b *Block) *Block {
	if b == nil {
		return nil
	}
	out := &Block{P: b.P, Stmts: make([]Stmt, len(b.Stmts))}
	for i, s := range b.Stmts {
		out.Stmts[i] = CloneStmt(s)
	}
	return out
}

func CloneStmt(s Stmt) Stmt {
	switch x := s.(type) {
	case *Var:
		c := *x
		c.Init = CloneExpr(x.Init)
		return &c
	case *Destructure:
		c := *x
		c.Names = append([]string(nil), x.Names...)
		c.Init = CloneExpr(x.Init)
		return &c
	case *ExprStmt:
		c := *x
		c.Expr = CloneExpr(x.Expr)
		return &c
	case *Return:
		c := *x
		c.Value = CloneExpr(x.Value)
		return &c
	case *Block:
		return CloneBlock(x)
	case *If:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		c.Then = CloneStmt(x.Then).(*Block)
		if x.Else != nil {
			c.Else = CloneStmt(x.Else)
		}
		return &c
	case *While:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *For:
		c := *x
		if x.Init != nil {
			c.Init = CloneStmt(x.Init)
		}
		c.Cond = CloneExpr(x.Cond)
		if x.Step != nil {
			c.Step = CloneStmt(x.Step)
		}
		if b, ok := x.Body.(*Block); ok {
			c.Body = CloneBlock(b)
		} else {
			c.Body = CloneStmt(x.Body)
		}
		return &c
	case *Match:
		c := *x
		c.Tag = CloneExpr(x.Tag)
		c.Arms = make([]*MatchArm, len(x.Arms))
		for i, arm := range x.Arms {
			ac := *arm
			ac.Guard = CloneExpr(arm.Guard)
			ac.Body = CloneBlock(arm.Body)
			ac.TupleElems = append([]TuplePatElem(nil), arm.TupleElems...)
			c.Arms[i] = &ac
		}
		return &c
	}
	return s
}

func CloneExpr(e Expr) Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *Ident:
		c := *x
		return &c
	case *NumberLit:
		c := *x
		return &c
	case *FloatLit:
		c := *x
		return &c
	case *BoolLit:
		c := *x
		return &c
	case *StringLit:
		c := *x
		return &c
	case *Binary:
		c := *x
		c.Left = CloneExpr(x.Left)
		c.Right = CloneExpr(x.Right)
		return &c
	case *Unary:
		c := *x
		c.Operand = CloneExpr(x.Operand)
		return &c
	case *Call:
		c := *x
		c.Callee = CloneExpr(x.Callee)
		c.Args = make([]Expr, len(x.Args))
		for i, a := range x.Args {
			c.Args[i] = CloneExpr(a)
		}
		c.TypeArgs = append([]Type(nil), x.TypeArgs...)
		return &c
	case *Index:
		c := *x
		c.Array = CloneExpr(x.Array)
		c.Idx = CloneExpr(x.Idx)
		return &c
	case *SliceExpr:
		c := *x
		c.Source = CloneExpr(x.Source)
		c.Low = CloneExpr(x.Low)
		c.High = CloneExpr(x.High)
		return &c
	case *FieldAccess:
		c := *x
		c.Target = CloneExpr(x.Target)
		return &c
	case *TryOp:
		c := *x
		c.Inner = CloneExpr(x.Inner)
		return &c
	case *IfExpr:
		c := *x
		c.Cond = CloneExpr(x.Cond)
		c.Then = CloneExpr(x.Then)
		c.Else = CloneExpr(x.Else)
		return &c
	case *MatchExpr:
		c := *x
		c.Tag = CloneExpr(x.Tag)
		c.Arms = make([]*MatchExprArm, len(x.Arms))
		for i, arm := range x.Arms {
			a := *arm
			if arm.Guard != nil {
				a.Guard = CloneExpr(arm.Guard)
			}
			a.Body = CloneExpr(arm.Body)
			a.TupleElems = append([]TuplePatElem(nil), arm.TupleElems...)
			c.Arms[i] = &a
		}
		return &c
	case *ArrayLit:
		c := *x
		c.Elems = make([]Expr, len(x.Elems))
		for i, el := range x.Elems {
			c.Elems[i] = CloneExpr(el)
		}
		return &c
	case *TupleLit:
		c := *x
		c.Elems = make([]Expr, len(x.Elems))
		for i, el := range x.Elems {
			c.Elems[i] = CloneExpr(el)
		}
		return &c
	case *Lambda:
		c := *x
		c.Params = append([]Param(nil), x.Params...)
		c.Captures = append([]Param(nil), x.Captures...)
		c.Body = CloneBlock(x.Body)
		return &c
	case *StructLit:
		c := *x
		c.Fields = make([]FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			c.Fields[i] = FieldInit{Name: f.Name, Value: CloneExpr(f.Value)}
		}
		c.TypeArgs = append([]Type(nil), x.TypeArgs...)
		return &c
	case *CastExpr:
		c := *x
		c.Inner = CloneExpr(x.Inner)
		return &c
	case *Assign:
		c := *x
		c.Target = CloneExpr(x.Target)
		c.Value = CloneExpr(x.Value)
		return &c
	}
	return e
}

func (f *FuncType) String() string {
	out := "("
	for i, p := range f.Params {
		if i > 0 {
			out += ", "
		}
		// A param type can be nil when an upstream inference step
		// bailed out (e.g. `use x <- f()` whose callback type the
		// checker couldn't pin — see inferUseParam). Render it as
		// `<unknown>` rather than dereferencing nil: this String is
		// reached while formatting a diagnostic (E038), so a panic
		// here would mask the real error.
		if p == nil {
			out += "<unknown>"
			continue
		}
		out += p.String()
	}
	out += ") => "
	if f.Result == nil {
		return out + "<unknown>"
	}
	return out + f.Result.String()
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
// wasm32 (4-byte pointers). `u8` uses 1 byte, and i64 / u64 /
// f64 use 8 bytes. Pointer-shaped types (string / Array / struct / enum
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
	case DynTraitType:
		// `dyn Trait` representation is target-dependent
		// (docs/DYN-TRAITS.md §4.2.1/§4.2.2):
		//   - wasm (ptrW==4): inline two-word `[data, vtable]` fat
		//     pointer, so an element occupies two pointer-width slots —
		//     same stride as a two-word string.
		//   - natives (ptrW==8): boxed one-word — a `dyn` value is a
		//     single heap pointer to a `{data, vtable}` cell, so it
		//     strides one pointer width like any other pointer.
		if ptrW == 4 {
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

// RcReuseEnabled gates the constructor-reuse (FBIP) layer specifically — the
// self-overwrite reuse (tryStructReuseOverwrite / tryEnumReuseOverwrite) AND
// the general reuse token (computeReuseSources, threaded drop→alloc). It rides
// ON top of RcFreeEnabled (reuse only makes sense when freeing), but is a
// SEPARATE axis so the differential gate can pin reuse-on == reuse-off
// byte-identical OUTPUT — isolating a reuse bug from a plain free bug. Default
// on; the Test{X86_64,Arm64,WASM}ReuseMatchesNoReuse gate flips it off for the
// baseline. Turning it off only DISABLES the optimisation (every reuse site
// falls back to a fresh alloc + the normal drop), so it can never change
// observable behaviour — that invariant is exactly what the gate asserts.
// EnumRcPayloads (Slice 1b, docs/OWNERSHIP-INFERENCE-PLAN.md) makes enum
// construction rc-count its pointer payloads exactly like StructLit/TupleLit —
// an aliased payload is inc'd (box co-owns), a moved last-use OWNED-LOCAL
// payload is move-marked; an own-PARAM payload is inc'd and balanced by the
// exit-sweep dec (same as a struct field). This gives enum boxes counted
// payload references, which dissolves the escape-taint + preciseDroppable
// exclusions that block FBIP enum precise drops. Off by default until the
// differential gate pins on==off byte-identical + the suite is green.
var EnumRcPayloads = true

// OwnedByDefault (Slice 2, docs/OWNERSHIP-INFERENCE-PLAN.md) flips parameter
// ownership toward the Koka/Perceus model: a parameter is OWNED by the callee
// (the caller retains it with an inc at the call site, the callee reclaims it
// with a dec at exit) rather than borrowed, so an ordinary function can reclaim
// its argument when it holds the last reference — no `own` annotation needed.
// rc is invisible, so the differential gate pins on == off byte-identical
// output; the reclaim is the only effect. Rolled out per param-type category
// (enums first — immutable, so the inc can't disturb the in-place-mutation
// semantics the borrow model exists for); a borrow-inference optimization that
// keeps read-only non-escaping params borrowed rides on top in a later slice.
// OFF by default until the model is complete and the suite is green.
var OwnedByDefault = true

// BorrowInferEnabled (Slice 2 / borrow inference, docs/OWNERSHIP-INFERENCE-PLAN.md)
// is the optimization that rides on top of OwnedByDefault: a parameter that the
// escape analysis (inferParamEscapes) proves does NOT escape the callee is kept
// BORROWED instead of owned — the caller skips the retain inc and the callee
// skips the exit dec, since the value can't outlive the call frame and the
// caller still owns it (a fresh-temp arg is reclaimed by the caller's arg-temp
// path). This removes the inc/dec pair for the common reader (`len`/`sum`/field
// fold) called on a live local — the step that makes `own` truly redundant. Like
// the other rc axes it can never change observable output (a balanced inc/dec
// pair elided on a non-escaping value), so the differential gate pins on == off
// byte-identical. Default on.
var BorrowInferEnabled = true

var RcReuseEnabled = true

// RcReuseDropGuided selects the ICFP 2022 "frame-limited / drop-guided"
// SOURCE-SELECTION strategy inside computeReuseSources (plan item E3,
// docs/NICHE-BORROWS-PLAN.md — an EVALUATION axis behind a flag, not a
// default flip). It is ORTHOGONAL to RcReuseEnabled: reuse must be on for
// either strategy to run, and with this flag OFF the selection is the
// existing PLDI-2021-style pairing, byte-identical to before the flag
// existed. With it ON, reuse tokens are derived from DROP POINTS: a token
// is born where a donor D's last use ends, flows FORWARD along
// straight-line control flow within the frame (including into a dominated,
// non-loop if/match arm), and is claimed by the FIRST same-class
// construction C reached; tokens die at joins they cannot soundly cross
// and at frame exit. Every proposed pair still passes the identical gates
// (reuseClassOf class match, freeEligible, never-reassigned, name-unique,
// not moved / borrow-source) and the shared lowering's runtime is_unique
// guard + degrade-to-fresh-alloc, so like the other rc axes it can never
// change observable output — only WHICH pairs are proposed. Default off
// (evaluation only); settable via the FERN_RC_REUSE_DROP_GUIDED=1 env var
// so test subprocesses and the CLI can toggle it without a fork.
var RcReuseDropGuided = os.Getenv("FERN_RC_REUSE_DROP_GUIDED") == "1"

// SanitizeEnabled is the single opt-in surface for the debug
// memory-safety runtime (#5545) — the "sanitizer build" that turns the
// scattered, individually-named heap detectors into one coherent mode.
// Set it and the three heap checks below light up together:
//
//	LeakCheckEnabled  — leak census at exit
//	RcUnderflowTrap   — rc over-release (double free), reported + fatal
//	RcFreeDebug       — use-after-free quarantine, reported + fatal
//
// It deliberately does NOT enable RcTrace: that is per-heap-event
// stderr output, a tool you point at a reduced repro, not something a
// standing mode can afford. The individual flags stay settable on their
// own for exactly that kind of narrow probe; this one is what you reach
// for when you don't yet know which check will fire.
//
// The two failing checks ABORT with a named diagnostic on stderr
// (`fern-sanitizer: …` — see the backends' sanAbort) rather than only
// bumping a counter or dying on a bare `ud2`, so a sanitizer run says
// what went wrong without a debugger attached; the SIGILL is still
// there underneath for the gdb backtrace that says where.
//
// Integers need no sanitizer here — they are total and never-trap by
// policy (docs/INTEGER-SEMANTICS.md), so there is no integer-UB to
// catch. The surface is purely the heap/rc correctness that Perceus's
// manual inc/dec makes possible to get wrong.
//
// Zero release cost: with the flag off every check is unemitted and the
// asm is byte-identical to a build without the feature. Settable via
// FERN_SANITIZE=1 (the LeakCheckEnabled precedent) or the CLI's
// -sanitize; the CLI path assigns this var and calls ApplySanitize.
var SanitizeEnabled = os.Getenv("FERN_SANITIZE") == "1"

// ApplySanitize folds SanitizeEnabled into the component detector
// flags. Called from this package's init for the env-var path, and
// again by the CLI after -sanitize parses — the component flags are
// plain vars read directly by the backends, so a late SanitizeEnabled
// assignment has to be pushed down rather than derived.
//
// It only ever turns flags ON: an individual FERN_* flag already set
// stays set, and clearing SanitizeEnabled does not un-apply.
func ApplySanitize() {
	if !SanitizeEnabled {
		return
	}
	LeakCheckEnabled = true
	RcUnderflowTrap = true
	RcFreeDebug = true
}

func init() { ApplySanitize() }

// LeakCheckEnabled gates the native leak detector (#5362 slice 1): a
// compile-time build mode that counts every __fern_alloc (count +
// 16-rounded bytes) and every __fern_free (count + identically rounded
// bytes) in BSS quads and prints one summary line to stderr at process
// exit — both the `_start` epilogue and the `exit()` builtin's
// __fern_exit:
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// where K = alloc_bytes − free_bytes. Both sides count the SAME
// (size+15)&-16 rounding, so a block's alloc and eventual free always
// cancel exactly and live_bytes is exact, not approximate.
// __fern_alloc_reuse's in-place path counts as NEITHER an alloc nor a
// free (see the emitter comments). x86-64 + arm64; wasm ignores the
// flag. With the flag OFF the emitted asm is byte-identical to a build
// without the feature. Settable via FERN_LEAKCHECK=1 (the
// RcReuseDropGuided precedent) so the CLI can toggle it without a fork,
// or implied by SanitizeEnabled — which also adds a leak VERDICT line
// (`fern-sanitizer: leak <K> bytes in <N> blocks`) after the summary
// when live_bytes is non-zero, so a sanitizer run doesn't need the
// numbers read to say whether it was clean.
var LeakCheckEnabled = os.Getenv("FERN_LEAKCHECK") == "1"

// RcFreeDebug turns the freelist into a use-after-free DETECTOR
// (x86-64 and arm64; a diagnostic build mode, set alongside
// RcFreeEnabled). Instead of recycling a freed array buffer, the
// free sites poison its rc word with RcPoison and quarantine the
// block. NOTHING is recycled in this mode — __fern_free declines the
// freelist push outright, since a reused block would overwrite its own
// poison — so __fern_alloc just keeps bumping.
//
// __fern_rc_inc / __fern_rc_dec then report and die the moment they
// touch a poisoned block — i.e. a stale reference to an over-released
// buffer — naming the holder the rc undercounted. Any helper that
// INLINES an rc op rather than calling out needs its own copy of the
// check (arm64's __fern_str_inc is the one such site: it has to
// preserve the (data, len) pair, so it cannot tail-call the helper);
// miss one and a stale reference walks straight past the detector.
//
// Settable via FERN_RC_FREE_DEBUG=1 (the LeakCheckEnabled precedent) so a
// probe binary can be built with the detector without a fork — the leak
// counters say a block was never freed, this says a live block was — or
// implied by SanitizeEnabled.
//
// A quarantined block still counts as a FREE for the leak census: the
// quarantine sites poison the rc word and then run the ordinary
// reclamation path, and it is __fern_free that skips the freelist push.
// Accounting where the release happens rather than where the memory is
// recycled is what lets the two detectors run together — otherwise
// every correctly-freed array reads as a leak.
var RcFreeDebug = os.Getenv("FERN_RC_FREE_DEBUG") == "1"

// SandboxEnabled installs a seccomp-bpf filter at `_start` permitting
// exactly the syscalls the emitted binary can issue, killing the
// process on anything else (x86-64 Linux only; #6071).
//
// The allowlist is the backend's recorded syscall set — see
// x86_64.EmitWithSyscalls. Deriving it from the emitted text rather
// than from capability declarations is the whole point: caps.Analyze
// models user-callable builtins, not the runtime's own mmap /
// exit_group / write / clock_gettime, so a caps-derived filter would
// need a hand-maintained floor that silently rots the moment the
// runtime grows a syscall. A filter derived from what was actually
// emitted cannot omit a syscall the program can make.
//
// This does NOT replace the compile-time capability system, which
// already rejects out-of-grant reach with E070. It is exploitation
// hardening: static analysis proves what the code CAN CALL, seccomp
// constrains what the process can do once control flow has been
// hijacked — the case a use-after-free in the rc runtime (the class
// RcFreeDebug exists for) could otherwise open.
//
// Opt-in via FERN_SANDBOX=1 (the LeakCheckEnabled precedent). Default
// off, and the emitted asm is byte-identical to a build without the
// feature when off. Defaulting it on waits on the whole fixture corpus
// running clean under it — an over-tight filter is a crash, not a
// warning, so the burden of proof sits on the feature.
var SandboxEnabled = os.Getenv("FERN_SANDBOX") == "1"

// RcTrace makes every heap event self-describing (x86-64 only; a
// diagnostic build mode). __fern_alloc and __fern_free each write one
// line to stderr:
//
//	rctrace <a|f> <ptr> <size> <site>
//
// all three numbers fixed-width 16-digit hex, `a` = alloc, `f` = free.
// `site` is the RETURN ADDRESS of the alloc/free call — i.e. the code
// that asked for the memory, not the helper that handed it out — so
// `-g` plus addr2line names the source line directly.
//
// This is to LeakCheckEnabled what RcUnderflowTrap is to the underflow
// counter: the counter says a leak happened, this says WHERE. A
// `leakcheck: ... live_bytes=4096` line is a true statement nothing can
// act on; pairing the trace's allocs against its frees leaves exactly
// the sites that allocated memory the program never gave back. The two
// compose — run both and the summary tells you how much to look for.
//
// Fixed-width hex is deliberate: the consumer is a pairing script, and
// a uniform record needs no field-splitting to match an `a` line to its
// `f` line by pointer. Note the addresses are RUNTIME addresses, which
// match a `-g` symtab directly for the default (non-PIE) x86-64 target;
// under -pie subtract the load base first.
//
// Settable via FERN_RC_TRACE=1 (the RcFreeDebug precedent). With the
// flag OFF the emitted asm is byte-identical to a build without the
// feature — every emission site is gated, nothing is left behind.
var RcTrace = os.Getenv("FERN_RC_TRACE") == "1"

// RcUnderflowTrap turns the Phase 3 over-release COUNTER into a TRAP
// (x86-64 and arm64; a diagnostic build mode). Every site that bumps
// __fern_rc_underflow — the inline dec, the __fern_rc_dec /
// __fern_arr_dec / __fern_map_drop helpers — follows the bump with
// `ud2`, so the process dies with SIGILL at the exact dec that
// over-released and a gdb backtrace names the function.
//
// This is the companion RcFreeDebug is not: RcFreeDebug quarantines
// FREED array/map blocks and traps a later touch, so it only sees an
// over-release that went through a free. A plain __fern_rc_dec taking a
// count 1 → 0 frees nothing, and the next dec on that block underflows
// with no quarantined block to trip over — invisible to RcFreeDebug,
// counted by __rc_underflow_count(), and located by nothing until this.
//
// The trap is a `call __fern_san_abort` carrying a fixed message, not a
// bare `ud2`: the process still dies of SIGILL (inside the abort
// helper, one frame above the offending dec, so the backtrace is
// unchanged) but stderr now says WHAT died. x86-64 and arm64.
//
// Settable via FERN_RC_UNDERFLOW_TRAP=1 (the RcFreeDebug precedent) or
// implied by SanitizeEnabled.
var RcUnderflowTrap = os.Getenv("FERN_RC_UNDERFLOW_TRAP") == "1"

// TrmcEnabled gates tail-recursion-modulo-cons. A function whose recursive
// call sits in the LAST payload position of a constructor in tail position
// (the canonical `map(xs) -> Cons(g(h), map(t))` shape) is normally
// NOT tail-recursive — the constructor wraps the recursive result — so it
// grows the stack O(n) and overflows on long lists. TRMC rewrites such a
// function into a "hole-passing" loop: each node is allocated with its
// recursive field left as a hole, the previous hole is filled with the new
// node, and the hole advances to the new node's field — O(1) stack, single
// pass, no reversal. Like RcReuseEnabled it is a SEPARATE axis from the
// behaviour it optimises: turning it off only disables the rewrite (the
// function lowers as ordinary recursion), so it can never change observable
// output — the Test{X86_64,Arm64,WASM}Trmc*MatchesNoTrmc gates assert exactly
// that byte-identical invariant. Default on.
var TrmcEnabled = true

// RcPoison is the rc-word marker a quarantined (freed) block carries
// in RcFreeDebug mode: a large positive value that can't be a real
// refcount and isn't the high-bit static sentinel.
const RcPoison = 0x7EEDFACE

// MapHeaderBytes is the size of a core/map kv-buffer header — the bytes
// before the bucket array. Layout: cap+0, len+4, keyKind+8, valTag+12,
// hashSeed+16, pad+20 (see core/map.fern's module header).
//
// It is the SINGLE Go-side spelling of a constant that also exists in Fern
// as `__map_hdr_bytes`, and the two must agree exactly: the Fern runtime
// allocates and indexes the buffer, while the Go side both frees it
// (__fern_map_drop, once per backend) and walks its entry column (the
// generated __drop_map_* loops in internal/ir). Disagreeing by 8 bytes
// makes every column walk read the entry array off by two slots, which
// presents as a SEGV in the drop path rather than as anything resembling a
// layout bug — and it does so on arm64 first, because its 16-byte entry
// stride puts the misread further out than wasm32's 8.
//
// A test pins the two spellings together (internal/e2e's map header check),
// so widening the header again means changing both and nothing else.
const MapHeaderBytes = 24

// CodegenMu serialises native codegen calls that read or
// write `TwoWordOverride`. arm64.Emit toggles the flag during
// its Emit body; x86_64.Emit reads it via `ir.LowerWith`.
// Before this mutex existed, parallel arm64 emits could
// stack their toggles such that one goroutine's defer
// restored the flag to `false` while another goroutine was
// still mid-emit — producing single-word `string_from_bytes_unchecked`
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
	case StringType, StrType, ArrayType, SliceType, TupleType,
		StructType, EnumType:
		return true
	case *FuncType:
		return true
	case DynTraitType:
		// A trait object is pointer-shaped (a tagged value in the
		// interpreter; a two-word fat pointer on compiled backends —
		// see docs/DYN-TRAITS.md §4.2).
		return true
	}
	return false
}

// ReceiverTypeName maps a type to the surface name its methods are
// registered and dispatched under — struct/enum names as written, and
// one name per scalar method surface (`string` for both `string` and a
// `str` view, `char`, `boolean`, and the numeric widths). It backs the
// `__method_<Type>_<name>` mangling, the call-site dispatch, the
// `print` auto-`to_string` gate, and monomorph's `T.f()` associated-call
// rewrite. Returns false for types that can't carry methods.
//
// This is the ONE copy of the width switch on purpose: add a width here
// and every site above learns it at once. Open-coding it per site means
// each copy has to learn each new width independently, and a copy that
// misses one misdispatches silently (#5629).
func ReceiverTypeName(t Type) (string, bool) {
	switch rt := t.(type) {
	case StructType:
		return rt.Name, true
	case EnumType:
		return rt.Name, true
	case StringType:
		return "string", true
	case StrType:
		// `str` (#4813) shares the `string` method surface: every string
		// receiver method (builtin len/as_bytes and the std/string family)
		// dispatches on a view too -- methods borrow their receiver.
		return "string", true
	case CharType:
		// `char` (#5629) has its OWN method surface -- deliberately not
		// i32's, so the byte classifiers can never be reached through a
		// scalar. Nothing declares a `char` receiver yet.
		return "char", true
	case NumberType:
		switch {
		case rt.NormalWidth() == 64 && rt.IsSigned():
			return "i64", true
		case rt.NormalWidth() == 64 && !rt.IsSigned():
			return "u64", true
		case rt.NormalWidth() == 8:
			return "u8", true
		case !rt.IsSigned():
			return "u32", true
		default:
			return "i32", true
		}
	case FloatType:
		if rt.NormalWidth() == 64 {
			return "f64", true
		}
		return "f32", true
	case BoolType:
		return "boolean", true
	default:
		return "", false
	}
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
	case NeverType:
		_, ok := b.(NeverType)
		return ok
	case StringType:
		_, ok := b.(StringType)
		return ok
	case StrType:
		_, ok := b.(StrType)
		return ok
	case CharType:
		_, ok := b.(CharType)
		return ok
	case FloatType:
		y, ok := b.(FloatType)
		return ok && x.NormalWidth() == y.NormalWidth()
	case ArrayType:
		y, ok := b.(ArrayType)
		return ok && Equal(x.Elem, y.Elem)
	case StreamType:
		y, ok := b.(StreamType)
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
	case SelfType:
		_, ok := b.(SelfType)
		return ok
	case DynTraitType:
		y, ok := b.(DynTraitType)
		if !ok || len(x.Traits) != len(y.Traits) {
			return false
		}
		// Both are kept sorted+deduped at construction, so an
		// element-wise compare is order-insensitive: `dyn A + B` ≡
		// `dyn B + A`. Generic trait-args (parallel Args) must match
		// too, so `dyn Container[i32]` ≠ `dyn Container[string]`.
		for i := range x.Traits {
			if x.Traits[i] != y.Traits[i] {
				return false
			}
			xa, ya := x.ArgsFor(i), y.ArgsFor(i)
			if len(xa) != len(ya) {
				return false
			}
			for j := range xa {
				if !Equal(xa[j], ya[j]) {
					return false
				}
			}
			// Pinned associated-type bindings (sorted by name at
			// construction) must match too: `dyn P[Item = i32]` ≠
			// `dyn P[Item = string]` ≠ `dyn P` (unpinned).
			xb, yb := x.AssocFor(i), y.AssocFor(i)
			if len(xb) != len(yb) {
				return false
			}
			for j := range xb {
				if xb[j].Name != yb[j].Name || !Equal(xb[j].Type, yb[j].Type) {
					return false
				}
			}
		}
		return true
	case HandleType:
		y, ok := b.(HandleType)
		return ok && x.Resource == y.Resource && x.Borrowed == y.Borrowed
	case ProjType:
		y, ok := b.(ProjType)
		return ok && x.Name == y.Name && Equal(x.Base, y.Base)
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
	// Raw is the literal exactly as the source spelled it, suffix
	// excluded, and is set only when that spelling is not the decimal
	// rendering of Value — today, a `0x…` hex literal. Only the
	// formatter reads it, so a literal's base survives `-fmt`; every
	// other consumer works from Value.
	Raw string
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

// DowncastExpr is `expr as? Type` — a fallible downcast of a
// `dyn Trait` value to a concrete type. `Inner` must be a
// `dyn Trait`; `Target` a concrete struct/enum that implements that
// trait. It evaluates to `Option[Target]`: `Some(v)` when `Inner`'s
// runtime concrete type is exactly `Target`, else `None`. This is the
// runtime-checked counterpart to coercion (docs/DYN-TRAITS.md §9), and
// is deliberately a separate node from CastExpr (which is specialised
// for numeric truncate/extend) so the two never share lowering paths.
type DowncastExpr struct {
	P      Position
	Inner  Expr
	Target Type
	// Trait is the PRIMARY trait name of `Inner`'s `dyn Trait` type,
	// filled by the checker for the single-trait (vtable-pointer-compare)
	// codegen. Empty before checking.
	Trait string
	// Traits is the WHOLE trait set of `Inner`'s `dyn Trait` type (sorted,
	// == Trait for a single-trait dyn). Compiled downcast codegen keys the
	// vtable-pointer compare by this whole set (dynVtableSetKey): a
	// single-trait `dyn A` selects `__vtable_<A>_<T>` (byte-identical to
	// before), a multi-trait `dyn A + B` selects the MERGED
	// `__vtable_<A+B>_<T>` cell a multi-trait coercion of T stores, so the
	// compare is exact for any trait set (docs/DYN-TRAITS.md §10). The
	// interpreter handles any set by runtime concrete-type tag.
	Traits []string
}

// BlockExpr is a block used in value position: `{ stmt; stmt; …; tailExpr }`.
// The statements run first, in a fresh child scope, then the trailing
// expression `Tail` (written WITHOUT a `;`) is the block's value. A block
// whose final element is a `;`-terminated statement has no trailing
// expression (`Tail == nil`) and therefore no value (type `void`); using
// such a block where a value is required is a checker error.
//
// Produced for `if`/`match` *expression* branches (the `{ … }` after
// `if (cond)` / `else` / a match arm `=>`) and, since #4521, general
// value-position `{ … }` blocks. Lowered on every native backend
// (interp / wasm / arm64 / x86-64) via the IR. When the statements
// always exit early (`return` / `break` / `continue`) the block has no
// trailing value (`Tail == nil`) and its type is `never`, not `void`
// (#4522). See docs/BLOCK-EXPRESSIONS.md.
type BlockExpr struct {
	P     Position
	Stmts []Stmt
	Tail  Expr // value expression; nil for a value-less (void) block
}
type BoolLit struct {
	P     Position
	Value bool
}

// UnitLit is `()` — the sole value of type void, and the only way to
// write one. It exists so a type that has to name "no interesting
// payload" can do so: `Result[(), IoError]` is the shape every fallible
// operation with nothing to hand back returns, and `Ok(())` builds it.
//
// A void-returning *call* is not a value (`Ok(f())` is E072) — the
// literal is the one spelling, so backends never have to invent a slot
// for a value that was never pushed.
type UnitLit struct {
	P Position
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
	// Raw is the literal AS WRITTEN, so `-fmt` re-emits the author's
	// spelling instead of rendering Value back through %g — which
	// rewrote `1e-6` to `1e-06`, `1e100` to `1e+100` and trimmed
	// `0.7615941560` to `0.761594156` (#6802). Empty for a literal the
	// compiler synthesised (constfold's folded results), which prints
	// from Value as before.
	Raw string
	// Width is set by the checker once a concrete float type is
	// known (`var x: f32 = 1.5` → 32). 0 means the literal stayed
	// unsettled; every consumer (interp, IR lowering) defaults it
	// to f64, the language's primary float — see
	// FloatType.NormalWidth.
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
	// it to pick a stride (1 byte for `[u8]`, 4 for `[i32]` /
	// `[u32]` / `[f32]` / pointers, 8 for `[i64]` / `[u64]` /
	// `[f64]`) and to choose between i32.store / i32.store8 /
	// i64.store / f32.store / f64.store. nil falls back to the
	// historical 4-byte-per-element layout.
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
	// Unchecked, when set, tells the IR to lower an ARRAY index
	// (not string/slice) without the per-access bounds check —
	// the caller has statically proven `0 <= Idx < len(Array)`.
	// Currently set only by DesugarForEachArray on its synthetic
	// `iter[idx]` element read: `idx` starts at 0, steps +1, the
	// loop guard is `idx < iter.len()` (captured once), and both
	// `iter` and `idx` are compiler-generated names user code
	// cannot touch — and Fern arrays never shrink in place — so
	// the access is provably in bounds every iteration (#4380
	// lever 3). Honoured on the array path only; string/slice
	// indexing ignores it and keeps its check.
	Unchecked bool
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
	// ArgNames, when non-nil, is parallel to Args: ArgNames[i] is the
	// parameter name a named argument `name = expr` targets, or "" for a
	// positional argument. nil when the call is all-positional (the common
	// case). The internal/defaultargs pass reorders named arguments into
	// positional order and fills defaults, then clears ArgNames — so the
	// checker and every later pass only ever see a positional Args list.
	ArgNames []string
	// IsPipe is set by the parser when this Call was synthesised
	// from a `LHS |> Callee(args...)` pipe expression: Args[0] is
	// the original LHS, Args[1:] are the original explicit args.
	// All later passes treat IsPipe-flagged calls identically to
	// any other Call; only the formatter checks the flag so it
	// can re-render the pipe form on the way out.
	IsPipe bool
	// PipeHole records where the pipe's LHS landed when the piped
	// call used the `_` topic placeholder (`x |> f(a, _)` — LHS
	// substitutes at the hole instead of being prepended). It is
	// the 1-BASED index into Args of the substituted slot; 0 means
	// "no placeholder, LHS was prepended as Args[0]" (the default
	// data-first form). Like IsPipe, only the formatter reads it —
	// to re-render `x |> f(a, _)` instead of the prepended form.
	PipeHole int
	// TypeArgs holds the callee's instantiation when it resolves to
	// a generic function (FuncDecl with non-empty TypeParams) — one
	// entry per type parameter, in declaration order, empty for a
	// non-generic call. The parser fills it from an explicit
	// `f[i32](x)` type-argument list; otherwise the checker fills it
	// with what it inferred. The monomorphisation pass uses it to
	// pick the right cloned function and rewrite the callee name to
	// the mangled form.
	TypeArgs []Type
	// TypeArgsWritten distinguishes parser-filled TypeArgs from
	// checker-inferred ones, so the formatter reprints only what the
	// source actually wrote. See StructLit.TypeArgsWritten.
	TypeArgsWritten bool
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
	// DynTrait is set by the checker to the trait name when this Call
	// is a method call on a `dyn Trait` receiver (`d.area()` where
	// `d: dyn Shape` ⊢ DynTrait = "Shape"). Such a call is dispatched
	// at runtime by the receiver value's concrete type rather than
	// statically rewritten to `__method_<Type>_…`; the callee stays a
	// FieldAccess. Monomorph leaves these untouched and the interpreter
	// resolves the concrete method from the runtime tag. Empty for
	// ordinary (statically dispatched) calls. See docs/DYN-TRAITS.md.
	DynTrait string
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
	// EqCall is set by the checker when `==` / `!=` is applied to a
	// composite type (struct / enum) that implements `Eq`: the
	// operator desugars to the type's structural `eq` method. The
	// IR lowers this Call instead of an identity-comparing OpEq, so
	// `a == b` means value equality, not heap-pointer equality.
	// EqNegate is set for `!=` (lower as `!a.eq(b)`).
	EqCall   *Call
	EqNegate bool
	// CmpCall is set by the checker when an ordering operator
	// (`<` `<=` `>` `>=`) is applied to a composite type that
	// implements `Ord`: the operator desugars to `a.cmp(b) <op> 0`
	// (cmp returns -1/0/1). The post-check rewrite replaces the
	// Binary with that scalar comparison; Op is preserved.
	CmpCall *Call
	// ArithCall is set by the checker when an arithmetic operator
	// (`+` `-` `*` `/`) is applied to a composite type (struct / enum)
	// whose conventionally-named method exists (`+`→add, `-`→sub,
	// `*`→mul, `/`→div) — operator overloading. The post-check rewrite
	// replaces the Binary with this method call, so every later pass
	// sees an ordinary call. See #2706.
	ArithCall *Call
	// CheckedLowered is set by the checker for the checked integer
	// operators (`+?` `-?` `*?`, #5542): the operator desugars to a
	// block-expr that yields `Some(result)` when it fits the operand
	// type and `None` on overflow. The post-check rewrite replaces
	// the Binary with this expression, so every later pass — interp,
	// codegen — sees an ordinary `Option[T]`-valued expression rather
	// than a bespoke opcode. Mirrors the EqCall / ArithCall channel.
	CheckedLowered Expr
}
type Unary struct {
	P       Position
	Op      string
	Operand Expr
	// IsFloat is set by the checker when the operand is a float,
	// so codegen can pick the f32 form of the operation.
	IsFloat bool
	// NegCall is set by the checker when unary `-` is applied to a
	// composite type (struct / enum) with a `neg` method — operator
	// overloading (`-v` → `v.neg()`). The post-check rewrite replaces the
	// Unary with this call. See #2706.
	NegCall *Call
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
	// Lowered, when non-nil, is a desugared replacement for this `?`
	// built + checked by the checker and swapped in by the post-check
	// rewrite. Used for the error-converting `?` on a `Result[_, E]`
	// inside a function returning `Result[_, dyn Trait]` (E implements
	// Trait): it lowers to a block-expr that maps the error to
	// `dyn Trait` then applies an ordinary `?`. See #3234.
	Lowered Expr
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
	// TypeArgs holds the instantiation of a generic struct
	// (StructDecl with non-empty TypeParams) — one entry per type
	// parameter, in declaration order. The parser fills it from an
	// explicit `Box[i32] { … }` type-argument list; otherwise the
	// checker fills it with what it inferred from the field values.
	// The monomorphisation pass uses it to mangle TypeName into
	// `<base>__<arg1>__<arg2>` and clear the field. After
	// monomorph runs, every StructLit has TypeArgs empty.
	TypeArgs []Type
	// TypeArgsWritten distinguishes the parser-filled TypeArgs
	// above from the checker-inferred ones: a written instantiation
	// is authoritative, so a destination type must not settle the
	// literal's fields to a different one behind the user's back.
	TypeArgsWritten bool
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
	// PathSep records that the source used the path separator `::`
	// (`Type::method`, `mod::func`) rather than `.`. Purely cosmetic — the
	// checker / modload treat both identically; it only lets the printer
	// round-trip the separator the author wrote. See #2700.
	PathSep bool
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
	// ReturnUnannotated records that an arrow lambda was written without a
	// `: R` return type (`(x) => expr`), so the checker infers ReturnType
	// from the body expression instead of defaulting to void. Mirrors
	// FuncDecl.ReturnUnannotated; see checker.inferReturns.
	ReturnUnannotated bool
	// Arrow records that the source wrote the arrow form `(x) => expr`
	// rather than `function(x) { … }`. Both parse to the same node, so
	// only the formatter reads this — without it a formatted arrow lambda
	// comes back as a `function` whose return type had to be invented.
	Arrow bool
	Body  *Block
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

func (e *NumberLit) Pos() Position    { return e.P }
func (e *CastExpr) Pos() Position     { return e.P }
func (e *DowncastExpr) Pos() Position { return e.P }
func (e *BlockExpr) Pos() Position    { return e.P }
func (e *BoolLit) Pos() Position      { return e.P }
func (e *UnitLit) Pos() Position      { return e.P }
func (e *StringLit) Pos() Position    { return e.P }
func (e *FString) Pos() Position      { return e.P }
func (e *FloatLit) Pos() Position     { return e.P }
func (e *Ident) Pos() Position        { return e.P }
func (e *ArrayLit) Pos() Position     { return e.P }
func (e *Index) Pos() Position        { return e.P }
func (e *SliceExpr) Pos() Position    { return e.P }
func (e *Call) Pos() Position         { return e.P }
func (e *Binary) Pos() Position       { return e.P }
func (e *Unary) Pos() Position        { return e.P }
func (e *Assign) Pos() Position       { return e.P }
func (e *IfExpr) Pos() Position       { return e.P }
func (e *MatchExpr) Pos() Position    { return e.P }
func (e *TryOp) Pos() Position        { return e.P }
func (e *StructLit) Pos() Position    { return e.P }
func (e *TupleLit) Pos() Position     { return e.P }
func (e *MapLit) Pos() Position       { return e.P }
func (e *FieldAccess) Pos() Position  { return e.P }
func (e *EnumLit) Pos() Position      { return e.P }
func (e *CaptureRef) Pos() Position   { return e.P }
func (e *MakeClosure) Pos() Position  { return e.P }
func (e *Lambda) Pos() Position       { return e.P }

func (*NumberLit) isExpr()    {}
func (*CastExpr) isExpr()     {}
func (*DowncastExpr) isExpr() {}

// String renders the downcast in source form, `<inner> as? <Target>`.
func (e *DowncastExpr) String() string {
	return fmt.Sprintf("%v as? %s", e.Inner, e.Target)
}

func (*BlockExpr) isExpr() {}

// String renders the block in source form, `{ <N stmts> <tail> }`.
// Statements aren't Stringers, so they're summarised by count; the tail
// (an Expr) renders in full.
func (e *BlockExpr) String() string {
	tail := "void"
	if e.Tail != nil {
		tail = fmt.Sprintf("%v", e.Tail)
	}
	if len(e.Stmts) == 0 {
		return fmt.Sprintf("{ %s }", tail)
	}
	return fmt.Sprintf("{ <%d stmt(s)>; %s }", len(e.Stmts), tail)
}
func (*BoolLit) isExpr()     {}
func (*UnitLit) isExpr()     {}
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
	// Sugar records the `for … in …` loop this Block is the desugar of, so
	// the formatter reprints the loop the user wrote instead of the lowered
	// index loop and its synthetic bindings. Nil on every other Block, and
	// read by the printer only — the walk deliberately skips it, since its
	// Body is the same node the lowered loop already carries.
	Sugar *ForEach
}
type If struct {
	P    Position
	Cond Expr
	Then Stmt
	Else Stmt // may be nil
	// IsAssert marks an `assert(cond[, msg])` desugar (parseAssert), so the
	// `-O` elision pass can drop the whole check (mirrors Loop.IsTodo's
	// marker precedent). Asserts must be side-effect-free — elision removes
	// the condition evaluation along with the check.
	IsAssert bool
}

type While struct {
	P    Position
	Cond Expr
	Body Stmt
	// Label is the optional loop label (`outer: while (...) { ... }`),
	// empty when unlabeled. A labeled `break`/`continue` names it to
	// target this loop from a nested one.
	Label string
}

// Loop is the canonical unconditional infinite loop (`loop { ... }`).
// Unlike While, it carries no Cond — it is definitionally diverging:
// every control-flow path through it either loops forever or exits via
// `break`/`return`, never by falling off the end. That makes it the
// vehicle divergence analyses (blockDiverges/stmtDiverges,
// funcBodyExits) key off, instead of pattern-matching a literal-true
// While condition. `break`/`continue` (labeled or not) work as in any
// While loop.
type Loop struct {
	P    Position
	Body Stmt
	// Label is the optional loop label (`outer: loop { ... }`), empty
	// when unlabeled.
	Label string
	// IsTodo marks a Loop synthesised by the parser's `todo;` /
	// `todo("msg");` desugar (`loop { eprint(...); exit(101); }`).
	// The `loop` shape gives the stub divergence for free (E052
	// missing-return + `let else` both already treat Loop as
	// non-falling-through), and the flag lets the formatter
	// round-trip the sugar instead of printing the desugared body.
	// TodoMsg holds the ORIGINAL message expression (nil for the
	// bare `todo;` form) — the formatter re-prints it verbatim.
	// Both fields are inert everywhere else (checker / IR / interp
	// see an ordinary Loop).
	IsTodo  bool
	TodoMsg Expr
}

// For preserves the C/JS-style three-part for loop so that `continue`
// can jump to the step *before* re-checking the condition.
type For struct {
	P    Position
	Init Stmt // may be nil
	Cond Expr // required
	Step Stmt // may be nil
	Body Stmt
	// Label is the optional loop label (see While.Label).
	Label string
}

// ForEach is the un-desugared `for IDENT in Iter Body` loop (the plain,
// non-range, non-map-tuple form). The parser emits it instead of desugaring at
// parse time, so a later type-aware pass can choose the lowering by Iter's type:
// an array/string/slice → the `.len()` + index loop; a `stream[T]` → a lazy
// per-element read loop. ID gives the desugar unique helper-var names.
// See docs/STREAM-TYPE-SURFACE.md.
type ForEach struct {
	P      Position
	ID     int
	Var    string
	VarPos Position
	Iter   Expr
	Body   Stmt
	Label  string
	// RangeHigh marks the range form `for i in LOW..HIGH`: Iter is LOW and
	// RangeHigh the bound, with RangeIncl selecting `..=`. That form is
	// desugared at parse time, so a ForEach carrying it exists only as a
	// Block's Sugar.
	RangeHigh Expr
	RangeIncl bool
	// Var2 is the value binder of the map form `for (K, V) in m` — Var is
	// the key. Empty for every other form.
	Var2 string
}

// DesugarForEachArray lowers a ForEach over an array/string/slice to the
// `.len()` + index C-style loop — the exact shape the parser used to build at
// parse time (moved here so a type-aware pass owns the choice of lowering). The
// step lives on the For (not appended to the body) so `continue` still advances
// the index; the index decls live on the enclosing block so an outer loop does
// not re-zero them.
func DesugarForEachArray(fe *ForEach) *Block {
	kw := fe.P
	iterName := fmt.Sprintf("__foreach_iter_%d", fe.ID)
	idxName := fmt.Sprintf("__foreach_idx_%d", fe.ID)
	lenName := fmt.Sprintf("__foreach_len_%d", fe.ID)
	mkIdent := func(name string) *Ident { return &Ident{P: kw, Name: name} }
	mkNum := func(v int64) *NumberLit { return &NumberLit{P: kw, Value: v} }

	declIter := &Var{P: kw, Name: iterName, Init: fe.Iter}
	declLen := &Var{P: kw, Name: lenName, Init: &Call{P: kw, Callee: &FieldAccess{P: kw, Target: mkIdent(iterName), Field: "len", FieldPos: kw}}}
	declIdx := &Var{P: kw, Name: idxName, Init: mkNum(0)}
	// The element read is provably in bounds — idx starts at 0, the loop
	// guard is `idx < len` (len captured once from iter), idx/iter are
	// synthetic names, and Fern arrays never shrink in place — so mark it
	// Unchecked to drop the per-iteration bounds check (#4380 lever 3).
	// Honoured only when the checker resolves it to an ARRAY index; string
	// iteration keeps its __str_idx check.
	bindUser := &Var{P: fe.VarPos, Name: fe.Var, Init: &Index{P: fe.VarPos, Array: mkIdent(iterName), Idx: mkIdent(idxName), Unchecked: true}}
	stepStmt := &ExprStmt{P: kw, Expr: &Assign{P: kw, Target: mkIdent(idxName), Value: &Binary{P: kw, Op: "+", Left: mkIdent(idxName), Right: mkNum(1)}}}

	innerStmts := []Stmt{bindUser}
	if blk, ok := fe.Body.(*Block); ok {
		innerStmts = append(innerStmts, blk.Stmts...)
	} else {
		innerStmts = append(innerStmts, fe.Body)
	}
	forLoop := &For{
		P:     kw,
		Cond:  &Binary{P: kw, Op: "<", Left: mkIdent(idxName), Right: mkIdent(lenName)},
		Step:  stepStmt,
		Body:  &Block{P: kw, Stmts: innerStmts},
		Label: fe.Label,
	}
	return &Block{P: kw, Stmts: []Stmt{declIter, declLen, declIdx, forLoop}, Sugar: fe}
}

// StreamElemKind returns the canonical kind string for a scalar stream element
// type — `u8` / `i32` / `i64` / `f64` etc. — used to name the per-element-type
// codegen helper `__stream_elem_<kind>` (and to register its FuncSig). It is the
// single source of truth shared by the desugar (here), the checker (sig
// registration), and wasmbin (helper emission), so all three agree on the name.
// Returns "" for a non-scalar element (strings / structs / enums are not
// lazily iterable yet — they collect eagerly, same as the eager path).
func StreamElemKind(t Type) string {
	switch n := t.(type) {
	case NumberType:
		if n.Signed {
			return "i" + strconv.Itoa(n.NormalWidth())
		}
		return "u" + strconv.Itoa(n.NormalWidth())
	case FloatType:
		return "f" + strconv.Itoa(n.NormalWidth())
	}
	return ""
}

// DesugarForEachStream lowers a LAZY stream `for x in f(args) { BODY }` — where
// `f` is an `@import async function f(): stream[T]` for a scalar `T` — to a real
// Fern loop that pulls one element at a time off the wire
// (docs/STREAM-TYPE-SURFACE.md, L2). Unlike the array form
// (collect-then-iterate), this never materialises the whole sequence: it opens
// the stream once (allocating a small heap "cursor" holding the readable handle
// + a one-element buffer), reads-and-awaits a single element per turn, and stops
// at EOF. It expands to
//
//	{
//	    var __stream_c_<ID> = f$open(args);            // cursor pointer
//	    while (true) {
//	        if (__stream_next(__stream_c_<ID>) == 0) { break; }   // 0 = EOF
//	        var x: T = __stream_elem_<kind>(__stream_c_<ID>);     // buffered element
//	        BODY
//	    }
//	    __stream_drop(__stream_c_<ID>);
//	}
//
// The callees (`f$open`, `__stream_next`, `__stream_elem_<kind>`, `__stream_drop`)
// are codegen helpers the checker registers FuncSigs for and wasmbin emits (see
// internal/codegen/wasmbin/extern.go). Separating the EOF flag (`__stream_next`
// → 0/1) from the value read (`__stream_elem_<kind>`) is what makes this work for
// ANY scalar element — unlike a single `i32` with a `-1` EOF sentinel, which is
// unambiguous only for `u8` — so `for x in` over an `i32` / `i64` / `f64` stream
// lowers the same way. A real `while` means `break` / `continue` / `return` /
// labels in BODY all work; the per-turn read advances the cursor, so `continue`
// re-reads the next element.
func DesugarForEachStream(fe *ForEach, elemType Type) *Block {
	kw := fe.P
	call := fe.Iter.(*Call)
	openName := call.Callee.(*Ident).Name + "$open"
	cName := fmt.Sprintf("__stream_c_%d", fe.ID)
	elemFn := "__stream_elem_" + StreamElemKind(elemType)
	mkIdent := func(name string) *Ident { return &Ident{P: kw, Name: name} }

	declC := &Var{P: kw, Name: cName, Init: &Call{P: kw, Callee: mkIdent(openName), Args: call.Args}}
	breakOnEOF := &If{
		P: kw,
		Cond: &Binary{P: kw, Op: "==",
			Left:  &Call{P: kw, Callee: mkIdent("__stream_next"), Args: []Expr{mkIdent(cName)}},
			Right: &NumberLit{P: kw, Value: 0}},
		Then: &Block{P: kw, Stmts: []Stmt{&Break{P: kw}}},
	}
	bindUser := &Var{P: fe.VarPos, Name: fe.Var,
		Init: &Call{P: fe.VarPos, Callee: mkIdent(elemFn), Args: []Expr{mkIdent(cName)}}}

	innerStmts := []Stmt{breakOnEOF, bindUser}
	if blk, ok := fe.Body.(*Block); ok {
		innerStmts = append(innerStmts, blk.Stmts...)
	} else {
		innerStmts = append(innerStmts, fe.Body)
	}
	loop := &While{
		P:     kw,
		Cond:  &BoolLit{P: kw, Value: true},
		Body:  &Block{P: kw, Stmts: innerStmts},
		Label: fe.Label,
	}
	drop := &ExprStmt{P: kw, Expr: &Call{P: kw, Callee: mkIdent("__stream_drop"), Args: []Expr{mkIdent(cName)}}}
	return &Block{P: kw, Stmts: []Stmt{declC, loop, drop}, Sugar: fe}
}

// Break / Continue carry an optional Label naming an enclosing labeled
// loop to target; empty means the innermost loop (the existing behaviour).
type Break struct {
	P     Position
	Label string
}
type Continue struct {
	P     Position
	Label string
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
//
// When OnError is set the statement is an `errdefer`: the
// cleanup runs only on an ERROR exit — the `?` operator
// propagating a None/Err, or a `return` whose value is a
// failure variant (None / Err) of an Option/Result-returning
// function. A plain success return or fall-off the end does
// NOT run it. (`errdefer` is Zig's rollback primitive: undo a
// partially-built value when init fails partway.) Everything
// else about the node — the active-flag machinery, LIFO order,
// the conditional-reached no-op — is identical to `defer`.
type Defer struct {
	P       Position
	Expr    Expr
	OnError bool
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
//
// Struct destructure `let Point { x, y } = expr;` reuses the same node
// with Fields non-nil (parallel to Names): Names[i] binds the struct
// field Fields[i] instead of tuple element i. StructName is the named
// struct type written in the pattern (checked against Init's type). For
// the tuple form Fields is nil and StructName is empty.
type Destructure struct {
	P          Position
	Names      []string
	Fields     []string // struct destructure: field projected for Names[i]; nil = tuple mode
	StructName string   // struct destructure: the named struct type in the pattern; "" = tuple mode
	Init       Expr
	TempName   string // checker-stamped: name of the synthesised tuple/struct-holding local.
}
type ExprStmt struct {
	P    Position
	Expr Expr
}

// Match dispatches on a tagged-union value. Match arms are patterns
// that bind payload fields into local names visible inside the arm
// body. Exhaustiveness is checked at type-check time: every variant
// of the scrutinee's enum type must appear, OR the arm list ends
// with a wildcard pattern (`_`).
// Origin values for a Match the parser synthesised from a
// pattern-binding form. `if let` and `let … else` differ only in where
// the success arm's body comes from — the then-block for one, the rest
// of the enclosing block for the other — and in the extra rule the
// checker applies (a `let … else` else branch must diverge).
const (
	OriginIfLet   = "if_let"
	OriginLetElse = "let_else"
)

type Match struct {
	P    Position
	Tag  Expr
	Arms []*MatchArm
	// StructMatch is the scrutinee's struct type name when this is a
	// match on a struct value (arms are struct patterns `S { x, y }`,
	// which bind fields irrefutably). Empty for enum / tuple / literal
	// matches. Stamped by the checker (checkStructMatch) so the IR and
	// interpreter lower the arms as struct field-binds rather than
	// enum-variant matches.
	StructMatch string
	// Origin marks a match the parser synthesised from a pattern-binding
	// form rather than one the programmer wrote — OriginIfLet /
	// OriginLetElse; empty for a real `match`. Either way the desugar is
	// `match (E) { PAT => { success }, _ => { else } }`, so the trailing
	// wildcard arm is the else branch, not something the source spelled —
	// the checker skips the unreachable-arm diagnostics on it and reports
	// the pattern-binding codes (E022 / E023) instead of the generic ones
	// a hand-written match of this shape would draw. The formatter reads
	// it back to re-render the original form.
	// Mirrors the self-host parser's StmtMatch.origin.
	Origin string
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
// TuplePatElem is one element of a tuple pattern `(p0, p1, …)` in a
// match arm — exactly one of: a binder name (binds the element in the
// arm's scope), the `_` wildcard (element ignored), or a literal
// (element compared by equality). See MatchArm.TupleElems.
type TuplePatElem struct {
	Name       string // binder; empty when IsWildcard or Literal != nil
	IsWildcard bool   // `_` element
	Literal    Expr   // literal element; nil otherwise
}

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
	// NamedFields marks a named-field pattern (`Rect { w, h }`): each
	// Bindings entry is a field name (the bound local takes that name).
	// The checker validates the names against the variant's FieldNames
	// and reorders Bindings + BindingTypes into declaration order, so
	// every later stage treats them positionally like a `Rect(w, h)`
	// pattern. False for the positional form.
	NamedFields bool
	// FieldNames runs parallel to Bindings for a named-field pattern
	// (`S { field: local }`): FieldNames[i] is the struct/variant field
	// projected, Bindings[i] the local it binds. For the shorthand
	// `S { x }`, FieldNames[i] == Bindings[i]. nil for non-named patterns.
	// (Rename is supported for struct matches; enum named-field variant
	// patterns stay shorthand — the checker rejects a rename there.)
	FieldNames []string
	IsWildcard bool // `_ => …`
	Literal    Expr // `0 => …` / `"yes" => …` / `true => …`; nil otherwise
	// RangeHi, when non-nil, marks a range pattern `lo..hi => …` /
	// `lo..=hi => …` on a scalar scrutinee: Literal holds the low bound,
	// RangeHi the high bound, and RangeInclusive distinguishes `..=`
	// (inclusive hi) from `..` (exclusive hi). Lowered to the compound
	// bound test `scr >= lo && scr <op> hi` on the same literal-match
	// path as `==` arms.
	RangeHi        Expr
	RangeInclusive bool
	// TupleElems is a tuple pattern `(p0, p1, …) => …` on a tuple-typed
	// scrutinee — one element per scrutinee tuple element (arity checked
	// by the checker). Nil for non-tuple patterns; mutually exclusive
	// with VariantName / IsWildcard / Literal. BindingTypes runs parallel
	// to TupleElems (the checker fills it with the scrutinee's element
	// types) so the IR picks the right per-element load width.
	TupleElems []TuplePatElem
	Guard      Expr // optional `when <expr>`; nil for unconditional arms
	// AtBinding is the `n` in an `@`-pattern `n @ <pattern> => …`: the whole
	// matched value is also bound to `n` (with the scrutinee's type) in the
	// arm scope, alongside whatever <pattern> binds. Empty for plain patterns.
	AtBinding string
	Body      *Block
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
	// StructMatch mirrors Match.StructMatch: the scrutinee struct type
	// name when the arms are struct patterns `S { x, y }`. Empty otherwise.
	StructMatch string
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
	NamedFields   bool     // named-field pattern `Rect { w, h }` — see MatchArm.NamedFields
	FieldNames    []string // parallel to Bindings for named-field patterns — see MatchArm.FieldNames
	IsWildcard    bool
	Literal       Expr // literal pattern; mutually exclusive with VariantName / IsWildcard
	// RangeHi / RangeInclusive — range pattern `lo..hi` / `lo..=hi`; see
	// MatchArm.RangeHi. Literal holds the low bound.
	RangeHi        Expr
	RangeInclusive bool
	// TupleElems is a tuple pattern on a tuple-typed scrutinee — see
	// MatchArm.TupleElems. BindingTypes runs parallel to it.
	TupleElems []TuplePatElem
	Guard      Expr
	// AtBinding — the `n` in `n @ <pattern>`; see MatchArm.AtBinding.
	AtBinding string
	Body      Expr
}

func (s *Block) Pos() Position                  { return s.P }
func (s *If) Pos() Position                     { return s.P }
func (s *While) Pos() Position                  { return s.P }
func (s *Loop) Pos() Position                   { return s.P }
func (s *For) Pos() Position                    { return s.P }
func (s *ForEach) Pos() Position                { return s.P }
func (s *Break) Pos() Position                  { return s.P }
func (s *Continue) Pos() Position               { return s.P }
func (s *Return) Pos() Position                 { return s.P }
func (s *Defer) Pos() Position                  { return s.P }
func (s *Var) Pos() Position                    { return s.P }
func (s *Destructure) Pos() Position            { return s.P }
func (s *ExprStmt) Pos() Position               { return s.P }
func (s *Match) Pos() Position                  { return s.P }
func (s *FuncDecl) Pos() Position               { return s.P }
func (s *FuncDecl) GenericName() string         { return s.Name }
func (s *FuncDecl) GenericTypeParams() []string { return s.TypeParams }

func (*Block) isStmt()       {}
func (*If) isStmt()          {}
func (*While) isStmt()       {}
func (*Loop) isStmt()        {}
func (*For) isStmt()         {}
func (*ForEach) isStmt()     {}
func (*Break) isStmt()       {}
func (*Continue) isStmt()    {}
func (*Return) isStmt()      {}
func (*Defer) isStmt()       {}
func (*Var) isStmt()         {}
func (*Destructure) isStmt() {}
func (*ExprStmt) isStmt()    {}
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
	// Own marks an OWNED (consuming) parameter — `function f(own x: T)`.
	// The caller transfers ownership; the callee may consume / reclaim / reuse
	// it (vs the default BORROWED param, where the caller keeps ownership). The
	// checker enforces affine use (consumed at most once per path — see
	// checkOwnedParams); the runtime ownership transfer + reuse it unlocks are
	// later slices. Always false for struct fields / borrowed params.
	Own bool
	// Default is the default value for an optional parameter —
	// `function listen(port: i32, backlog: i32 = 128)`. nil for a
	// required parameter (the common case) and for struct fields /
	// receivers. The `internal/defaultargs` pass fills it in at call
	// sites that omit the trailing argument, so the checker and every
	// later pass see a complete positional call. A parameter with a
	// Default may not be followed by a required (Default == nil)
	// parameter — the parser rejects that.
	Default Expr
}

// InlineHint is a source-level inlining directive on a function decl
// (`@inline` / `@noinline` — #4412 Rec §14).
type InlineHint int

const (
	InlineHintNone   InlineHint = iota // no attribute — heuristic decides
	InlineHintAlways                   // @inline — lift the size cap
	InlineHintNever                    // @noinline — never a candidate
)

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
	// Bounds maps a type-parameter name to the traits it is
	// constrained by — `function f[T: Display + Eq](…)` records
	// Bounds["T"] = ["Display", "Eq"]. The checker uses bounds to
	// (a) resolve method calls on a `T`-typed value against the
	// trait's signature inside the generic body and (b) verify each
	// concrete type argument implements the required traits at the
	// call site. Nil when the function has no bounded type params.
	// See docs/TRAITS.md.
	Bounds map[string][]string
	// BoundArgs carries the type arguments of a generic-trait bound,
	// parallel to Bounds: `function f[T: From[i32]](…)` records
	// BoundArgs["T"] = [[i32]] alongside Bounds["T"] = ["From"]. The
	// checker substitutes them into the bound trait's method signatures
	// (`from(v: T)` → `from(v: i32)`) when resolving a method on a
	// `T`-typed value. Nil when no bound carries type args. See docs/TRAITS.md.
	BoundArgs  map[string][][]Type
	Params     []Param
	ReturnType Type
	// ReturnUnannotated records that the source wrote no `: Type` after
	// the parameter list, so ReturnType was defaulted to void by the
	// parser. The checker uses it to INFER the return type from the
	// function's `return` expressions (when they unify to a single
	// type) instead of forcing void — an explicit `: void` keeps
	// ReturnUnannotated false. Synthetic decls (monomorph clones,
	// closure hoists, derive synth) leave it false, so they are never
	// re-inferred. See checker.inferReturns.
	ReturnUnannotated bool
	Body              *Block
	// ImportIface / ImportWITName bind a body-less `function` to a WIT
	// import via an `@import("wasi:iface@x.y.z", "wit-func-name")` attribute
	// (bring-your-own WIT, P4 — docs/WIT-BRING-YOUR-OWN.md). ImportIface is
	// the versioned interface; ImportWITName is the WIT function name (which
	// may contain dashes / `[method]…` and so can't be a Fern identifier).
	// Both empty for an ordinary function; when set, Body is nil and a call
	// lowers to a core wasm import of (ImportIface, ImportWITName).
	ImportIface   string
	ImportWITName string
	// StreamResultElem is set (to the element type) when an `@import async
	// function f(): stream[T]` is normalised to its colorless effective result
	// `T[]` — the checker rewrites ReturnType to `T[]` early and stashes `T` here
	// so codegen knows to use the incremental stream-collect ABI (stream.read +
	// the await loop) rather than the single-block list-result lowering. nil for
	// every other function. See docs/STREAM-TYPE-SURFACE.md.
	StreamResultElem Type
	// StreamParamElems maps a parameter index to its element type when an
	// `@import async` param is `stream[T]` — the checker rewrites that param to
	// `T[]` (the eager array the call site passes) and records `T` here, so codegen
	// streams the array's elements out over the wire (stream.new + write-await +
	// drop-writable) rather than passing a single list block. nil otherwise; the
	// mirror of StreamResultElem.
	StreamParamElems map[int]Type
	// ExportIface / ExportWITName bind a function (WITH a body) to a WIT
	// *export* via an `@export("wasi:iface@x.y.z", "wit-name")` attribute
	// (bring-your-own WIT, P6 — docs/WIT-BRING-YOUR-OWN.md): the component
	// provides this function as the named world export, lifted with the WIT
	// canonical ABI — the generalisation of the fixed `cli/run` (main) and
	// `incoming-handler` (handle) lifts to an arbitrary world export. Both
	// empty for an ordinary function; mutually exclusive with ImportIface.
	ExportIface   string
	ExportWITName string
	// Public marks this declaration as exported from its module.
	// Set by the parser when the source carries `pub function …`.
	// Default false (private) — modload rejects cross-module
	// references to non-public decls before the checker runs.
	Public bool
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// Receiver, when non-nil, marks this declaration as a method on
	// the struct type Receiver.Type.(StructType).Name. The checker
	// hoists methods into top-level functions under the mangled name
	// `__method_<Type>_<Name>` and rewrites `expr.Method(args)` call
	// sites to `__method_<Type>_<Method>(expr, args)` so codegen
	// never has to know about methods.
	Receiver *Param
	// MethodRecv / MethodSimpleName are stamped by the checker's
	// receiver-hoist (which consumes Receiver) so a later Check pass —
	// the monomorph re-check rebuilds Info from scratch, after Receiver
	// is already gone — can re-register the method in Info.Methods
	// without parsing the mangled name. MethodRecv is the canonical
	// receiver type name (e.g. "shapes__Square", "i32"); both are
	// empty for non-methods. See docs/TRAITS.md.
	MethodRecv       string
	MethodSimpleName string
	// AssocType, when non-empty, marks this as an associated function
	// of that (mangled) type name — a receiver-less `impl` method like
	// `function origin(): Self` in `impl … for Point`. Receiver stays
	// nil; the checker hoists it to `__assoc_<AssocType>_<Name>` and
	// resolves `Point.origin()` call sites (a FieldAccess on a type
	// name) to that flat name with no receiver argument. `Self` in the
	// signature is substituted to the impl type at parse time, exactly
	// like an ordinary impl method.
	AssocType string
	// IsLocal is true for functions declared as a statement inside
	// another function's body. Closure conversion at codegen time
	// hoists these to top-level entries and rewrites captured-var
	// references to read from a synthetic env argument.
	IsLocal bool
	// Fip marks a function the source annotated `fip function …` — a
	// Koka-style fully-in-place CHECKED guarantee. The checker (E053)
	// verifies the body performs no heap allocation (a sound, conservative
	// subset: in-place index writes to `own` array params are allowed, but
	// any allocating literal / string op / growing method / call to a
	// non-`fip` function is rejected). It is a verify-don't-enable
	// annotation — the in-place lowering already happens; `fip` only asserts
	// and checks it. Default false. Set by the parser for `fip function`.
	// The IR layer additionally verifies the claim against the ops it
	// actually emitted (E068, verifyFipAllocs) — see docs/REUSE-CONTRACT.md.
	Fip bool
	// Fbip marks a function the source annotated `fbip function …` — the
	// Koka-style "fully in place with borrowing" sibling of Fip. The checker
	// runs the same E053 walk with a RELAXED allocation rule: constructor
	// expressions (struct / tuple literals, payload-carrying enum variants)
	// are allowed, because the IR layer verifies each such site is
	// reuse-PAIRED (computeReuseSources / the self-overwrite hooks /
	// consumingMatchReuse) or covered by FipAllowance — an un-reused site
	// is an E068 error at lowering time (verify-and-enable, plan E2').
	// Mutually exclusive with Fip (parse error). Default false; set by the
	// parser for `fbip function`.
	Fbip bool
	// FipAllowance is the graded allowance `n` of `fip(n)` / `fbip(n)`: at
	// most n fresh (un-reused) constructor allocations are permitted by the
	// IR-level E068 verification. The bare forms are allowance 0. Stored by
	// the parser; the checker only relaxes the constructor-shape rule when
	// it is > 0 (the IR owns the count).
	FipAllowance int
	// Async marks a function the source annotated `async function …` —
	// the WASI Preview-3 component-model-async export surface. On
	// `-target wasm32-wasi -emit core-module` the driver lifts the async-marked function with
	// the `async` canonical option (result via `canon task.return`), so
	// the produced component exports it as `<name>: async func() ->
	// <result>`, runnable under
	// `wasmtime -W component-model-async,component-model-async-stackful`.
	// Default false; set by the parser for `async function`. See
	// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
	Async bool
	// InlineHint carries a source-level `@inline` / `@noinline`
	// attribute (#4412 Rec §14): Always lifts the IR inliner's size
	// cap for this function (shape-safety exclusions still apply);
	// Never excludes it from inlining entirely. None (the zero
	// value) leaves the heuristic in charge.
	InlineHint InlineHint
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
	// checker-synthesised decls leave this empty.
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
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// Opaque marks a `pub opaque struct` — the type name is exported
	// but its fields are private outside the declaring module: other
	// modules can hold/pass values and call methods, but cannot read
	// fields or construct via a struct literal. The checker enforces
	// this against the access site's SourceModule. See docs/TRAITS.md.
	Opaque bool
	// Derives lists the trait names from an `@derive(Trait, …)`
	// attribute on the struct. The checker synthesises an `impl`
	// per derived trait (field-wise) before conformance runs. See
	// docs/TRAITS.md.
	Derives []string
	// MustConsume marks a `@must_consume` struct: every value of
	// this type must be consumed (passed, returned, stored into a
	// marked container, or destructured) on every control-flow
	// path before its binding leaves scope — enforced by the
	// checker's E067 walk. Zero runtime cost; RC still does the
	// actual freeing. See docs/MUST-CONSUME.md.
	MustConsume bool
	// SourceModule mirrors FuncDecl.SourceModule — modload stamps
	// the canonical module path that declared this struct. The LSP
	// answers cross-module goto-definition queries with it (jump
	// from `util.Point` use site to `Point`'s declaration in
	// util.fern), and the checker scopes the nominal name by it: a
	// struct is only a concrete type for the modules that can see
	// it, so one module's `struct V` cannot capture another's `V`
	// type parameter (#6118). Empty for parser-only single-file
	// programs and checker-synthesised decls, which the checker
	// reads as unscoped.
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
	// Monomorphized marks a per-instantiation clone the monomorphizer
	// emitted for a generic enum with a composite payload (#3693), e.g.
	// `E__i32` from `enum E[U] { A(Box[U]) }`. Such clones share their
	// variant names (`E__i32.A` vs `E__string.A`), so the checker lets a
	// destination type disambiguate a bare variant reference among them —
	// a relaxation that applies ONLY to clones, never to user-written
	// enums (whose shared variant names still require qualification).
	Monomorphized bool
	// Derives lists the trait names from an `@derive(Trait, …)`
	// attribute on the enum. The checker synthesises a variant-wise
	// `impl` per derived trait. See docs/TRAITS.md.
	Derives []string
	// MustConsume mirrors StructDecl.MustConsume for `@must_consume`
	// enums (E067; docs/MUST-CONSUME.md). A `match` on the value is
	// its canonical consuming use.
	MustConsume bool
	// Public marks the enum as exported across modules. Same
	// semantics as FuncDecl.Public — `pub enum Foo { … }` lets
	// other modules name `Foo`, including its variants in match
	// patterns and constructors.
	Public bool
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// SourceModule mirrors FuncDecl.SourceModule. See StructDecl
	// for the cross-module-LSP rationale.
	SourceModule string
}

// EnumVariant is one constructor in an EnumDecl. An empty Payloads
// slice means the variant is constructed by bare name (`Red`).
//
// A variant is either POSITIONAL — `Square(f64, f64)`, constructed
// `Square(2.0, 3.0)` and matched `Square(w, h)` — or NAMED-FIELD —
// `Rect { w: f64, h: f64 }`, constructed `Rect { w: 2.0, h: 3.0 }` and
// matched `Rect { w, h }`. FieldNames is empty for the positional form
// and, for the named form, parallel to Payloads (FieldNames[i] is the
// name of the field whose type is Payloads[i]). The runtime layout is
// identical either way — payloads are laid out in declaration order — so
// names are purely a parse/check concern; the checker reorders named
// match bindings + constructor args into declaration order before codegen.
type EnumVariant struct {
	P          Position
	Name       string
	Payloads   []Type
	FieldNames []string
}

// ResourceDecl is a top-level `resource Name;` — a nominal WIT resource-handle
// type (P5 — docs/WIT-BRING-YOUR-OWN.md). An `@import("iface",
// "wit-resource-name")` attribute binds the Fern name to its WIT resource
// identity (ImportIface / ImportWITName); a later P5 slice uses that binding to
// call `[resource-drop]<wit-name>` when an owned handle goes out of scope. The
// type is referenced as `own Name` / `borrow Name` (HandleType); values are
// opaque i32 handles. The checker registers each in Info.Resources; no later
// pass sees ResourceDecl (handles erase to i32 before IR lowering).
type ResourceDecl struct {
	P             Position
	Name          string
	ImportIface   string
	ImportWITName string
	// Public marks the resource as exported across modules — same semantics
	// as FuncDecl.Public.
	Public bool
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// SourceModule mirrors FuncDecl.SourceModule (modload stamps the
	// declaring module path). Empty for parser-only single-file programs.
	SourceModule string
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
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// SourceModule is the canonical module path that declared this
	// union. modload stamps it during loadRecursive; the checker
	// propagates it to the synthesised EnumDecl so cross-module
	// variant-pattern qualifier checks have a comparable target.
	SourceModule string
}

// TraitDecl is a top-level `trait Name { <sig>; … }` declaration — a
// named set of method signatures. Each method's first parameter is
// `self: Self` (ast.SelfType). A type "implements" the trait via a
// matching ImplDecl; the checker validates conformance + coherence
// (see docs/TRAITS.md). Traits carry no runtime representation: once
// the checker has validated impls, later stages ignore them.
type TraitDecl struct {
	P       Position
	Name    string
	NamePos Position
	Methods []TraitMethod
	// TypeParams names the trait's own type parameters (`trait From[T]`).
	// A method signature refers to them; each `impl From[Arg] for Type`
	// binds them via ImplDecl.TraitArgs, and the conformance check
	// substitutes them when comparing the impl's methods to the trait's.
	// Empty for a non-generic trait. See docs/TRAITS.md.
	TypeParams []string
	// Supertraits names the traits this trait requires (`trait Ord: Eq +
	// Hash { … }`): an `impl Ord for T` is legal only if `T` also
	// implements every supertrait (transitively), and a `T: Ord` bound
	// grants access to the supertraits' methods too. Empty for a trait
	// with no supertraits. modload mangles these names like any other
	// trait reference. See docs/TRAITS.md.
	Supertraits []string
	// AssocTypes names the trait's associated types (`type Item;`), in
	// declaration order. A method signature refers to one as
	// `Self::Item` (a ProjType); each impl must bind it via
	// `type Item = …`. Empty for a trait with no associated types.
	// See docs/ASSOCIATED-TYPES.md.
	AssocTypes []string
	// Public marks the trait as exported — same semantics as
	// FuncDecl.Public / StructDecl.Public.
	Public bool
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
	// SourceModule mirrors StructDecl.SourceModule — modload stamps
	// the declaring module so the coherence (orphan-rule) check can
	// tell a local trait from an imported one. Empty for single-file
	// programs.
	SourceModule string
}

// TraitMethod is one signature in a TraitDecl. For an ordinary method
// Params[0] is `self: Self` (ast.SelfType{}); the remaining params +
// Result use SelfType wherever the source wrote `Self`. An *associated
// function* (`Assoc` true) has no `self` receiver — it's called as
// `Type.f(args)` rather than `value.f(args)` and typically constructs a
// `Self` (e.g. `function default(): Self`).
type TraitMethod struct {
	P      Position
	Name   string
	Params []Param
	Result Type
	Assoc  bool
	// Body, when non-nil, is a default implementation: the trait method
	// was written `function f(self: Self): T { … }` rather than ending
	// at a `;`. An `impl` that omits a defaulted method inherits a copy
	// of this body (with `Self` substituted to the impl type) — see the
	// checker's synthesizeTraitDefaults and docs/TRAITS.md. nil = an
	// abstract signature every impl must provide.
	Body *Block
}

// ImplDecl is a top-level `impl Trait for Type { <function>… }`. The
// parser desugars each method into an ordinary receiver-method
// FuncDecl (with `Self` replaced by the `for` type) and appends those
// to Program.Funcs, so modload + the checker's existing
// receiver-hoist + dispatch paths handle them unchanged. ImplDecl
// itself is the record the checker uses to verify the impl satisfies
// the trait and to enforce coherence. MethodNames lists the method
// names provided (in source order) for the conformance diagnostics.
type ImplDecl struct {
	P           Position
	Trait       string
	TraitPos    Position
	Type        Type
	TypePos     Position
	MethodNames []string
	// TraitArgs are the type arguments applied to a generic trait in
	// `impl From[i32] for Celsius` — bound positionally to the trait's
	// TraitDecl.TypeParams. The conformance check substitutes them into
	// the trait's method signatures. Empty for a non-generic trait. See
	// docs/TRAITS.md.
	TraitArgs []Type
	// TypeParams names the impl's own type parameters for a parametric
	// impl (`impl[T: Bound] Trait for Box[T]`). Empty for a plain
	// `impl Trait for ConcreteType`. The checker resolves occurrences
	// of these names inside Type to ParamType so the conformance
	// signature comparison lines up with the (generic) hoisted
	// methods. See docs/TRAITS.md.
	TypeParams []string
	// Bounds maps each impl type parameter to its trait bounds (from
	// `impl[T: Bound] …`). The parser passes these straight onto each
	// desugared method, so ImplDecl only needs them for methods the
	// checker synthesises later — a trait's default method inherited by
	// a parametric impl (synthesizeTraitDefaults). Nil for a plain impl.
	Bounds map[string][]string
	// AssocTypeBindings maps each of the trait's associated-type names to
	// the concrete type this impl binds it to (`type Item = i32;`). The
	// conformance check requires one entry per trait associated type; the
	// checker/monomorph resolve `Self::Item` / `T::Item` projections
	// through it. Nil for an impl of a trait with no associated types.
	// See docs/ASSOCIATED-TYPES.md.
	AssocTypeBindings map[string]Type
	// SourceModule is the module that wrote the impl — used by the
	// orphan-rule check (the impl is legal only if Trait or Type is
	// declared in this same module). Empty for single-file programs.
	SourceModule string
	// Methods holds the same desugared *FuncDecl values the parser also
	// appends to Program.Funcs — kept here purely so the formatter can
	// re-emit the `impl { … }` grouping (the checker/codegen path reads
	// them from Program.Funcs as before, unaware of this back-reference).
	// Each is the post-desugar form: `self` peeled into Receiver (or
	// AssocType stamped) and `Self` substituted to the impl type, so the
	// formatter renders the concrete type where the source wrote `Self`.
	// Empty for impls parsed before this field existed / with no methods.
	Methods []*FuncDecl
}

func (s *TraitDecl) Pos() Position { return s.P }
func (s *ImplDecl) Pos() Position  { return s.P }

type Program struct {
	Funcs   []*FuncDecl
	Structs []*StructDecl
	// TodoSites records the source position of every `todo;` /
	// `todo("msg");` statement the parser desugared, in source
	// order. `fern -check` prints a warning per entry so the
	// remaining stubs stay visible. Populated only by the parse
	// of THIS program — modload does not merge imported modules'
	// sites (Position carries no filename, so cross-module
	// entries could not be attributed correctly).
	TodoSites []Position
	// Resources lists top-level `resource Name;` declarations in source
	// order — nominal WIT resource-handle types (P5 — see ResourceDecl and
	// docs/WIT-BRING-YOUR-OWN.md). Referenced as `own Name` / `borrow Name`
	// (HandleType). The checker registers them in Info.Resources; the IR
	// never sees them (handles erase to i32 before lowering).
	Resources []*ResourceDecl
	// Traits lists top-level `trait` declarations in source order.
	// Impls lists `impl Trait for Type` declarations in source
	// order. Both are consumed by the checker (conformance +
	// coherence) and ignored by every later pass — see TraitDecl /
	// ImplDecl and docs/TRAITS.md.
	Traits []*TraitDecl
	Impls  []*ImplDecl
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
	// PubUses holds the module's `pub use "path".{…};` re-exports.
	PubUses []*PubUse
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
	// The checker consults this set to dedup stdlib loads — e.g. it
	// won't re-register `core/map`'s helpers when modload already
	// pulled the module in (directly or transitively).
	LoadedStdlibPaths map[string]bool
	// CapGrants records the capability grants declared in the loaded
	// manifests (docs/PACKAGE-CAPABILITIES-BRIEF.md phase 2), keyed by
	// the dependency package's resolved directory: for every dependency
	// entry carrying a `capabilities` key, the granted v1 capabilities
	// (sorted; the union when several manifests grant the same package).
	// A key mapping to an empty slice means `capabilities = []` (nothing
	// granted); a package directory absent from the map is ungoverned —
	// cmd/fern's enforcement warns instead of erroring for it
	// (warn-and-allow). modload populates this during loading; nil when
	// no manifest grants anything.
	CapGrants map[string][]string
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
	// PackageScoped marks a `pub(package)` declaration — visible to other
	// modules in the same package (same directory; the stdlib is one
	// package) but not exported to outside consumers. Mutually exclusive
	// with Public. See docs/PUB-PACKAGE.md.
	PackageScoped bool
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

// PubUse is a `pub use "path".{name1, name2};` re-export: the named
// public symbols of the target module become part of *this* module's
// public surface, so an importer of this module can reference them as
// `thismod.name` and they resolve to the original module's definition
// (no copy is made). modload loads the target like an import and records
// a per-module re-export table; the rewriter resolves a re-exported
// `mod.name` to the original mangled flat name. See docs/PRELUDE-TO-MODULES.md.
type PubUse struct {
	P     Position
	Path  string   // import path of the module being re-exported from
	Names []string // the public names re-exported (in source order)
}

func (d *PubUse) Pos() Position { return d.P }

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
