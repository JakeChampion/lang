package ast

// Ownership is the value-ownership axis of a type — orthogonal to a type's
// *shape* (String vs Array vs a struct). It is the compile-time counterpart of
// the runtime reference-count state a heap value carries (rc 1 = owned, rc > 1
// = shared, rc < 0 = immortal). Today ownership is scattered across three
// narrow carriers (`Param.Own`, the `inferParamEscapes` borrow side-table, and
// `HandleType.Borrowed`) and, for strings on the self-host path, reconstructed
// from syntactic heuristics; issue #4297 lifts it into a single type-carried,
// checker-enforceable fact. See docs/OWNERSHIP-TYPES-PLAN.md.
//
// This is the foundation slice (C1): the enum and the structural default. No
// pass reads it yet — later slices consolidate the existing carriers onto this
// axis and add checker enforcement.
type Ownership int

const (
	// Owned: this binding holds a counted reference. Aliasing it retains
	// (rc inc); dropping it releases (rc dec / free). The default for any
	// pointer-shaped value with no borrow evidence, and the (inert) default
	// for scalars, which carry no reference to track.
	Owned Ownership = iota
	// Borrowed: an owner elsewhere (a caller, a container) holds the counted
	// reference. Never retained on alias, never released on drop — freeing a
	// borrow would double-free the owner's reference. Default params are
	// borrowed under the owned-by-default model (see OwnedByDefault).
	Borrowed
	// View: aliases another value's backing storage (a slice / `.trim()`).
	// Its box must never free the shared buffer; the source must outlive it.
	// SliceType is the structural View today.
	View
	// Static: interned / immortal data (a string literal in .rodata, an
	// inline SSO value). Never freed. Distinct from View in that it aliases
	// nothing with a shorter lifetime — it simply lives forever.
	Static
)

func (o Ownership) String() string {
	switch o {
	case Owned:
		return "owned"
	case Borrowed:
		return "borrowed"
	case View:
		return "view"
	case Static:
		return "static"
	}
	return "owned"
}

// NeedsRC reports whether a value with this ownership participates in
// reference counting — i.e. whether an alias must retain and a drop must
// release it. Owned values do; Borrowed / View / Static do not (a caller owns
// the Borrowed reference, and View / Static must never be freed). This is the
// single predicate the inc/dec-insertion and drop passes will consult once
// ownership is type-carried, replacing the per-shape + side-table reasoning
// they do today.
func (o Ownership) NeedsRC() bool { return o == Owned }

// StructuralOwnership returns the ownership a type implies from its *shape
// alone*, before any inference. It is the seed the later inference /
// consolidation slices refine (e.g. a pointer-shaped parameter proven
// non-escaping is narrowed from Owned to Borrowed; a string bound from a
// literal is Static; a slice bound from `s[a:b]` is View).
//
//   - SliceType is the one structural View — `[T]` is a documented non-owning
//     view into an Array<T> (see ast.go's SliceType doc).
//   - HandleType carries its own borrow bit (`own R` / `borrow R`).
//   - Every other pointer-shaped type defaults to Owned.
//   - Scalars (number / bool / float / void / erased handle) hold no reference;
//     Owned is the inert default (NeedsRC() is false for them regardless, since
//     the RC passes gate on shape too).
func StructuralOwnership(t Type) Ownership {
	switch h := t.(type) {
	case SliceType:
		return View
	case StrType:
		// `str` (#4813) is the string-side structural View, the surface
		// citizen of this axis: a borrowed-string view must never be freed
		// by its holder.
		return View
	case HandleType:
		if h.Borrowed {
			return Borrowed
		}
		return Owned
	}
	return Owned
}

// ExprResultOwnership classifies the ownership of the value an expression
// *produces*, from the expression's syntax plus its resolved type `t`. This is
// the typed counterpart of the self-host path's syntactic freshness heuristics
// (`expr_is_fresh_str` / `str_local_binding_is_fresh` / …): it names, as one
// type fact, which producers yield a fresh owned value, which yield a view, and
// which yield an immortal literal.
//
// It resolves the cases that are unambiguous from syntax:
//   - a string literal is Static (interned .rodata / immortal);
//   - a string slice `s[a:b]` is Owned — the checker lowers it to a
//     copy-into-fresh-string (`__str_slice`), so it owns its buffer (IsString);
//   - an array slice `a[lo:hi]` is a View — it aliases the parent's storage;
//   - a fresh construction (array / struct / tuple / map literal) is Owned;
//   - a scalar literal is Owned (inert — NeedsRC() is false for it anyway);
//   - a call yields an Owned fresh result (the borrowed-return refinement, for
//     a callee that hands back one of its borrowed params, needs call-graph
//     info — a later slice; Owned is the common, conservative-for-leaks case).
//
// Everything else — a bare identifier / field read / index, whose ownership
// depends on the referent's binding (a borrowed param vs an owned local) —
// falls back to the type's StructuralOwnership. That fallback is a DEFAULT, not
// a sound reclaim decision on its own: refining an identifier to its binding's
// ownership needs the symbol table and is a later consolidation slice. Callers
// that must be sound about aliases (the RC-insertion passes) supply that
// context; callers that only need the producer classification (the checker's
// view/owned diagnostics) use this directly.
func ExprResultOwnership(e Expr, t Type) Ownership {
	return ExprResultOwnershipWith(e, t, nil)
}

// ExprResultOwnershipWith is ExprResultOwnership with binding-precise resolution
// of a bare identifier. `resolve` maps an in-scope name to its binding's
// ownership — a borrowed parameter, an owned local, a `View` local bound from a
// slice — returning (_, false) when the name is unknown. A caller with a symbol
// table (the checker, the RC-insertion passes) supplies it to make an identifier
// read reflect what it aliases rather than the type's structural default; a
// caller that only needs the producer classification passes nil (the
// ExprResultOwnership shorthand).
//
// The precise cases (literals, slices, fresh constructions, calls) are
// unchanged — they classify from syntax and don't consult `resolve`, since what
// a producer yields doesn't depend on any binding. Only the identifier fallback
// uses it.
func ExprResultOwnershipWith(e Expr, t Type, resolve func(name string) (Ownership, bool)) Ownership {
	switch ex := e.(type) {
	case *StringLit:
		return Static
	case *SliceExpr:
		if ex.IsString {
			return Owned // __str_slice copies into a fresh owned string
		}
		return View // array slice aliases the parent's storage
	case *ArrayLit, *StructLit, *TupleLit, *MapLit:
		return Owned // fresh construction, sole-owned
	case *NumberLit, *BoolLit, *FloatLit, *CharLit:
		return Owned // scalar (inert; NeedsRC() is false)
	case *Call:
		return Owned // fresh result (borrowed-return refinement is a later slice)
	case *Ident:
		if resolve != nil {
			if o, ok := resolve(ex.Name); ok {
				return o
			}
		}
	}
	return StructuralOwnership(t)
}
