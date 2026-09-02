package ir

// RC type-capability classification — THE single home for "what reference-
// counting capabilities does this type have?" (#4477, part of the #4393/#4474
// extraction). Keep them here rather than scattered across ir.go /
// rc_analysis.go / rc_insert.go, each answering one slice of the same
// question with its own edges; the goal-2 port mirrors this one file.
//
// The capability matrix, by type shape (see each predicate for the edges):
//
//	shape       | rc-tracked      | rc-tracked | owned-by-default        | needs deep drop
//	            | elem/capture    | local slot | (param inc/dec pair)    |
//	------------+-----------------+------------+-------------------------+------------------
//	i32/f64/... | no              | no         | no                      | no
//	string      | no (two-word)   | yes        | no                      | str_dec route
//	array       | yes             | yes        | no                      | arr_dec / per-elem
//	struct      | yes             | yes        | if string/array/Map-free| __drop_struct_*
//	tuple       | yes             | yes        | if string/array/Map-free| __drop_tuple_*
//	enum        | yes             | yes        | if eligible + uniform   | enumNeedsDrop
//	closure     | yes             | yes        | no                      | __closure_drop_*
//	dyn Trait   | dynRc-gated     | sweep-only | no                      | __drop_dyn_<set>
//	Map         | via struct name | via struct | no (isMapType excluded) | map drop glue
//
// Layering of the tracked sets (each a strict superset of the previous):
//
//	arrElemIsRcTracked  = {array, struct, enum, closure, tuple}
//	rcTrackedSlotType   = arrElemIsRcTracked + {string}
//	                      (also the counted-array-element set: see below)
//	exit-sweep tracked  = rcTrackedSlotType + {dyn Trait} (dynReclaim-gated,
//	                      see emitRcDecLocalsAtExitExcept — the safety zero
//	                      in lowerFunc deliberately uses rcTrackedSlotType)
//
// The name-keyed LOCAL classifiers (preciseDroppableType, isOwnedRcLocal)
// stay with their analyses in rc_analysis.go — they classify a local (name →
// declared type → capability), not a type.

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// rcTrackedSlotType reports whether a local/param SLOT of this type holds an
// rc-tracked pointer-shaped value — the set the entry safety-zero and the
// exit dec sweep agree on (strings included; dyn Trait is the sweep-only
// extra, gated on dynReclaim). Phase 1d covered arrays; 1e widened to
// structs, enums (a heap box or rc-headered sentinel), closures (heap pair /
// static cell), tuples (headered boxes), then strings (rc-headered heap
// buffers; the SSO inline-tag guard in __fern_rc_dec keeps short inline
// strings safe on every non-zero ptrW).
//
// It doubles as the COUNTED-ARRAY-ELEMENT set: `.append` (emitArrayPush) and
// `.with` (emitArraySet) both retain an aliased element, release the element
// they overwrite, and copy through a retaining grow/CoW helper for exactly
// these types, and the buffer's walk-drop releases them again. That is a
// wider set than arrElemIsRcTracked, which answers the narrower question of
// which elements __fern_drop_arr_ptr may dec with a single-word __fern_rc_dec
// — a string element needs the string-aware helper instead.
func rcTrackedSlotType(t ast.Type) bool {
	if _, isStr := t.(ast.StringType); isStr {
		return true
	}
	return arrElemIsRcTracked(t)
}

// arrElemIsRcTracked reports whether an array element type is a
// SINGLE-WORD pointer-shaped rc-tracked value — array / struct (incl.
// Map) / enum / closure. These are the elements __fern_drop_arr_ptr /
// __fern_arr_cow_inplace_ptr can walk with a bare __fern_rc_dec / _inc.
// Strings are excluded for shape, not for tracking: they ARE counted
// array elements (see rcTrackedSlotType), but a two-word (data, len)
// element carries its inline tag in `len`, so it needs the string-aware
// __fern_drop_arr_str / __fern_arr_cow_inplace_str walk instead.
// Primitive elements (i32 etc.) are not pointers, so no drop.
func arrElemIsRcTracked(elem ast.Type) bool {
	switch elem.(type) {
	case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType:
		return true
	}
	return false
}

// isMapType reports whether t is the runtime Map handle type. A
// Map-typed field / payload / capture reclaims its structure (value
// column + buf + handle) via __map_drop_values then __fern_map_drop,
// both of which self-guard on the map's own rc==1 and return the map
// ptr (so a stack value chains through).
func isMapType(t ast.Type) bool {
	st, ok := t.(ast.StructType)
	return ok && st.Name == "Map"
}

// typeIsStringArrayFree reports whether `t`'s deep-drop reclaims no string or
// array buffer — i.e. t is built transitively from scalars, enums, structs, and
// tuples only (no string / array / slice / Map, no unresolved generic). `seen`
// breaks recursive-type cycles (a self-recursive enum like List is fine: the
// back-edge is assumed free, and any string/array on a real payload is caught on
// its own first visit before the back-edge is taken).
func (b *builder) typeIsStringArrayFree(t ast.Type, seen map[string]bool) bool {
	return typeIsStringArrayFreeIn(b.info, t, seen)
}

// typeIsStringArrayFreeIn is typeIsStringArrayFree for a pass that runs
// before any builder exists (paramVerdictFacts).
func typeIsStringArrayFreeIn(info *checker.Info, t ast.Type, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true
	case ast.StringType, ast.ArrayType, ast.SliceType:
		return false
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !typeIsStringArrayFreeIn(info, e, seen) {
				return false
			}
		}
		return true
	case ast.StructType:
		if ty.Name == "Map" {
			return false
		}
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if !typeIsStringArrayFreeIn(info, f.Type, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen[ty.Name] {
			return true
		}
		seen[ty.Name] = true
		ed, ok := info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !typeIsStringArrayFreeIn(info, pl, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// typeDeepDropWired reports whether a value of type `t` is fully reclaimed by
// the generated deep-drop machinery (__drop_struct_ / __drop_enum_ /
// __drop_tuple_ / the array / string helpers) on EVERY backend — the
// precondition for a callee to own a value it did not build, which both an
// owned-by-default parameter and a consumed-threaded one release at exit. It
// allows scalars, strings, and arrays / structs / enums / tuples of wired
// types; it rejects Map (its deep drop is incomplete), slices, closures, and
// unknown / generic / runtime-handle types whose drop is not statically wired.
func typeDeepDropWired(t ast.Type, info *checker.Info, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType, ast.StringType:
		return true
	case ast.ArrayType:
		return typeDeepDropWired(ty.Elem, info, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if !typeDeepDropWired(e, info, seen) {
				return false
			}
		}
		return true
	case ast.StructType:
		if ty.Name == "Map" {
			return false
		}
		if seen["s:"+ty.Name] {
			return true
		}
		seen["s:"+ty.Name] = true
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false // runtime handle (Reader / Writer / MapIter) / unknown
		}
		for _, f := range sd.Fields {
			if !typeDeepDropWired(f.Type, info, seen) {
				return false
			}
		}
		return true
	case ast.EnumType:
		if seen["e:"+ty.Name] {
			return true
		}
		seen["e:"+ty.Name] = true
		ed, ok := info.Enums[ty.Name]
		if !ok {
			return false
		}
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				if !typeDeepDropWired(pl, info, seen) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func typeTransitivelyContainsMap(info *checker.Info, t ast.Type, seen map[string]bool) bool {
	switch ty := t.(type) {
	case ast.StructType:
		if ty.Name == "Map" {
			return true
		}
		if seen["s:"+ty.Name] {
			return false
		}
		seen["s:"+ty.Name] = true
		sd, ok := info.Structs[ty.Name]
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if typeTransitivelyContainsMap(info, f.Type, seen) {
				return true
			}
		}
		return false
	case ast.EnumType:
		return enumTransitivelyContainsMap(info, ty.Name, seen)
	case ast.TupleType:
		for _, e := range ty.Elems {
			if typeTransitivelyContainsMap(info, e, seen) {
				return true
			}
		}
		return false
	case ast.ArrayType:
		return typeTransitivelyContainsMap(info, ty.Elem, seen)
	case ast.SliceType:
		return typeTransitivelyContainsMap(info, ty.Elem, seen)
	}
	return false
}

func enumTransitivelyContainsMap(info *checker.Info, enumName string, seen map[string]bool) bool {
	if seen["e:"+enumName] {
		return false
	}
	seen["e:"+enumName] = true
	ed, ok := info.Enums[enumName]
	if !ok {
		return true // unknown / generic-erased — conservative (exclude)
	}
	for _, v := range ed.Variants {
		for _, pl := range v.Payloads {
			if typeTransitivelyContainsMap(info, pl, seen) {
				return true
			}
		}
	}
	return false
}

// isOwnedByDefaultType reports whether a parameter of type `t` is owned by the
// callee under Slice 2 (OwnedByDefault): the callee reclaims such a parameter
// at exit; the caller retains it with an inc at the call site.
func (b *builder) isOwnedByDefaultType(t ast.Type) bool {
	return ownedByDefaultTypeIn(b.info, t)
}

// ownedByDefaultTypeIn is isOwnedByDefaultType for a pass that runs before
// any builder exists (paramVerdictFacts).
func ownedByDefaultTypeIn(info *checker.Info, t ast.Type) bool {
	return ast.OwnedByDefault && ownedByDefaultShapeIn(info, t)
}

// ownedByDefaultShape is the TYPE half of isOwnedByDefaultType, independent of
// the flag: the set of types the owned model covers. computeConsumedParams
// reads it with the flag off too — a type the owned model would cover stays
// on the borrow baseline there, so the OwnedByDefault-vs-borrow differential
// compares the two models on the same set of parameters.
//
// Every admitted shape is immutable after construction (the checker rejects
// `p.x = v`; the idiom is a whole-value rebuild), so the caller-side retain
// inc can never disturb an in-place mutation through the parameter — there is
// none. What the callee's exit sweep then needs is a fully wired deep drop
// (typeDeepDropWired): arrays and strings included, so an enum carrying its
// children in an array (a HAMT / vector node) is owned like a box-only one,
// and a consuming match can hand the child array to its binding at the box's
// own count — the unique path then rewrites it in place. Map-bearing shapes,
// slices, closures and unresolved generics stay on the borrow model.
func (b *builder) ownedByDefaultShape(t ast.Type) bool {
	return ownedByDefaultShapeIn(b.info, t)
}

func ownedByDefaultShapeIn(info *checker.Info, t ast.Type) bool {
	switch ty := t.(type) {
	case ast.EnumType:
		// Boxes are rc-counted (enumRcPayloadsEligible). The layout need not
		// be uniform: the exit sweep's tag switch frees each variant at its
		// own size, and a consuming match knows its arm's variant
		// statically (enumVariantBoxSize); only the TRMC cell free still
		// wants one size and checks for it itself (trmcShapeConsumeSafe).
		return enumRcPayloadsEligibleIn(info, ty.Name) && typeDeepDropWired(t, info, map[string]bool{})
	case ast.StructType:
		// A real StructDecl (runtime handles — Map / Reader / Writer / MapIter
		// — have none and are rejected by typeDeepDropWired anyway); the box
		// is uniform by construction. Per-field rc counting (Phase
		// 1e-struct-ii) balances __drop_struct_<N>.
		if _, ok := info.Structs[ty.Name]; !ok {
			return false
		}
		return typeDeepDropWired(t, info, map[string]bool{})
	case ast.TupleType:
		// Uniform headered boxes whose elements are rc-counted (the
		// projection-site dup balances the per-element drop in emitDec's
		// tuple branch).
		return typeDeepDropWired(t, info, map[string]bool{})
	}
	return false
}

// enumRcPayloadsEligible reports whether the Slice-1b EnumRcPayloads model
// applies to enum `enumName`: the flag is on AND the enum's deep drop is fully
// wired on every backend. Enums whose payloads transitively contain a Map are
// excluded — a Map-in-enum deep drop calls `__map_drop_values`, a runtime helper
// the wasm helper-inclusion pass doesn't pull in for a generated `__drop_enum_`
// body, and Map key/value reclamation is itself an open gap. Excluded enums keep
// the move model (flag-off behaviour) at every site, a documented safe leak.
func (b *builder) enumRcPayloadsEligible(enumName string) bool {
	return enumRcPayloadsEligibleIn(b.info, enumName)
}

// enumRcPayloadsEligibleIn is enumRcPayloadsEligible for a pass that runs
// before any builder exists (inferParamCountedRetain).
func enumRcPayloadsEligibleIn(info *checker.Info, enumName string) bool {
	return ast.EnumRcPayloads && !enumTransitivelyContainsMap(info, enumName, map[string]bool{})
}

// enumRcPayloadsEligibleForValue is the expression form: true when `e` is a
// variant constructor (`Cons(..)`) or enum literal of an rc-eligible enum.
func (b *builder) enumRcPayloadsEligibleForValue(e ast.Expr) bool {
	var name string
	switch x := e.(type) {
	case *ast.Call:
		id, ok := x.Callee.(*ast.Ident)
		if !ok {
			return false
		}
		en, _, _, isVar := b.lookupVariantOn(id.Name, id.EnumName)
		if !isVar {
			return false
		}
		name = en
	case *ast.EnumLit:
		name = x.EnumName
	default:
		return false
	}
	return b.enumRcPayloadsEligible(name)
}

// enumNeedsDrop reports whether a concrete enum has a heap box worth
// reclaiming: at least one payload-carrying variant and no ParamType
// payload (generic). Mirrors enumVariantDropPlan's success condition
// without needing ptrW, so dropFnNameFor and the genEnumDropFn worklist
// agree on which enums get a __drop_enum_ fn.
func enumNeedsDrop(ed *ast.EnumDecl) bool {
	hasPayload := false
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if _, isParam := pt.(ast.ParamType); isParam {
				return false
			}
		}
		if len(v.Payloads) > 0 {
			hasPayload = true
		}
	}
	return hasPayload
}

// enumHasPointerPayload reports whether any variant of ed carries a
// POINTER-shaped payload (array / struct / enum / closure / Map — all
// heap-boxed). This is the condition for "the eligible enum drop should
// take the tag-dispatch variant-plan path rather than the branchless
// uniform path": every such payload is deep-droppable in a tag-guarded
// arm (where its exact type is known), where the uniform path could
// only flat-dec it (and a union's variants differ at the shared
// offset).
//
// It is NOT a test for "is this instantiation heap-boxed". An earlier
// revision of this comment claimed it was, and that scalar payloads are
// "pair-form, no box" — used to justify skipping the box_free for a
// scalar generic instantiation. Both halves were wrong: pair-form is a
// per-FUNCTION return ABI (findPairFormFuncs, keyed by function name),
// describing how a callee hands an Option back, not how an Option LOCAL
// is represented; and a local is boxed in every measured shape,
// including one bound from a pair-form-eligible callee. Option[i32] was
// leaking a 16-byte box per construction on that reasoning (#5917).
// emitEnumSlotDrop now adopts the substituted decl unconditionally, so
// this predicate is back to its one job: choosing the drop SHAPE.
func enumHasPointerPayload(ed *ast.EnumDecl) bool {
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if arrElemIsRcTracked(pt) {
				return true
			}
		}
	}
	return false
}
