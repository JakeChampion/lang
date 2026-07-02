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
	case HandleType:
		if h.Borrowed {
			return Borrowed
		}
		return Owned
	}
	return Owned
}
