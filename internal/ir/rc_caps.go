package ir

import "github.com/jakechampion/lang/internal/ast"

// The per-type RC capability table (#4477).
//
// The RC layer used to classify types through 7+ overlapping predicates
// spread across ir.go / rc_analysis.go / rc_insert.go — arrElemIsRcTracked,
// typeIsStringArrayFree, the typeTransitivelyContainsMap /
// enumTransitivelyContainsMap walkers, enumRcPayloadsEligible,
// preciseDroppableType, isOwnedByDefaultType, and the zero-init / exit-sweep
// rcTracked closures — each re-deriving a slice of the same question ("what
// RC capabilities does this type have?") with subtly different edges. This
// file is now the ONE place that question is answered: the axes are computed
// here, `rcCaps` aggregates them, and the legacy predicate names survive only
// as thin views so call sites keep their local vocabulary. Every predicate
// collapsed here is one fewer thing the goal-2 self-host port has to mirror.
//
// Two kinds of axis live here:
//   - SHAPE axes — a top-level type-switch (rcPtrShaped, rcZeroInitClass,
//     rcSlotTracked). Cheap; hot emit paths call these directly.
//   - TRANSITIVE axes — one shared walk of the type graph (rcTypeFacts)
//     answers both "is the deep drop string/array-free?" and "does the type
//     transitively contain a Map?" in a single pass, replacing the two
//     hand-rolled walkers that each re-implemented the recursion with their
//     own cycle-breaking and conservative defaults.

// rcCaps is the aggregated capability record for one type. `rcCapsOf`
// computes it with a single transitive walk; the derived verdicts
// (PreciseDroppable, OwnedByDefault) are built from the same facts so they
// can never drift from the axes they depend on.
type rcCaps struct {
	// PtrShaped: the value is a heap-boxed pointer wherever it is stored —
	// array / struct (incl. Map) / enum / closure / tuple. This is the set
	// __fern_drop_arr_ptr can safely dec as an array element, the set that
	// counts as an rc capture, and the pointer-payload test for enum drop
	// planning. Strings are deliberately excluded (their tracking is
	// ABI-dependent — see rcStringReclaimable); scalars are not pointers.
	PtrShaped bool
	// ZeroInit: the function-entry safety zero writes this local's slot so
	// the exit sweep's `ptr == 0` null guard fires on never-initialised
	// locals (conditional / match-arm declarations). PtrShaped plus strings.
	ZeroInit bool
	// SlotTracked: the function-exit dec sweep visits this local's slot.
	// ZeroInit plus `dyn Trait` on backends with a __drop_dyn_<set> helper
	// (b.dynReclaim) — the zero pass and the sweep must otherwise agree on
	// which slots they touch, which is why both read this table.
	SlotTracked bool
	// StringArrayFree: the deep drop reclaims no string or array buffer —
	// the type is built transitively from scalars, enums, structs, and
	// tuples only (no string / array / slice / Map, no unresolved generic).
	StringArrayFree bool
	// ContainsMap: the type transitively contains a Map. A Map-in-enum deep
	// drop calls `__map_drop_values`, a runtime helper the wasm
	// helper-inclusion pass doesn't pull in for a generated `__drop_enum_`
	// body, and Map key/value reclamation is itself an open gap — so
	// Map-containing enums keep the move model (a documented safe leak).
	ContainsMap bool
	// PreciseDroppable: an owned local of this type may take a precise drop
	// at its last use instead of the exit sweep. Arrays (every element kind
	// — slices 1–3), struct / Map / tuple boxes (slice 4), and rc-eligible
	// enums (Slice 1b, where construction rc-counts the pointer payloads so
	// the deep drop is rc-protected). See preciseDroppableType for the
	// name-keyed view and the soundness argument.
	PreciseDroppable bool
	// OwnedByDefault: a parameter of this type is owned by the callee under
	// Slice 2 (OwnedByDefault) — the callee reclaims it at exit and the
	// caller retains it at the call site. See isOwnedByDefaultType for the
	// per-category rollout rationale.
	OwnedByDefault bool
}

// rcCapsOf computes the full capability record for `t`: the shape axes, one
// transitive walk for the string/array and Map facts, and the derived
// verdicts built from those same facts.
func (b *builder) rcCapsOf(t ast.Type) rcCaps {
	free, hasMap := b.rcTypeFacts(t, map[string]bool{})
	c := rcCaps{
		PtrShaped:       rcPtrShaped(t),
		ZeroInit:        rcZeroInitClass(t),
		SlotTracked:     b.rcSlotTracked(t),
		StringArrayFree: free,
		ContainsMap:     hasMap,
	}
	c.PreciseDroppable = b.rcPreciseDroppable(t, hasMap)
	c.OwnedByDefault = b.rcOwnedByDefault(t, free, hasMap)
	return c
}

// ---------------------------------------------------------------------------
// Shape axes
// ---------------------------------------------------------------------------

// rcPtrShaped is the shape axis behind rcCaps.PtrShaped: heap-boxed pointer
// values — array / struct (incl. Map) / enum / closure / tuple.
func rcPtrShaped(t ast.Type) bool {
	switch t.(type) {
	case ast.ArrayType, ast.StructType, ast.EnumType, *ast.FuncType, ast.TupleType:
		return true
	}
	return false
}

// rcZeroInitClass is the shape axis behind rcCaps.ZeroInit: everything
// pointer-shaped, plus strings. Heap strings carry an rc header and the
// emitDec string branch reclaims owned ones via __fern_str_dec (two-word
// ABIs: wasm + arm64) or __fern_rc_dec (native single-word x86_64); the SSO
// inline-tag low-bit guard in __fern_rc_dec keeps short inline strings safe.
// A never-initialised slot must read as null so those helpers' null guards
// fire — which is exactly why the zero set and the sweep set share this axis.
func rcZeroInitClass(t ast.Type) bool {
	if rcPtrShaped(t) {
		return true
	}
	_, isStr := t.(ast.StringType)
	return isStr
}

// rcSlotTracked is the shape axis behind rcCaps.SlotTracked: the exit dec
// sweep's set. The zero-init class, plus `dyn Trait` values on backends that
// can reclaim them: a `dyn` slot owns its erased concrete `data` object
// (docs/DYN-TRAITS.md §4.4), dropped through the per-set __drop_dyn_<set>
// helper at scope exit — wasm (slice 4a) + x86-64 (slice 4b). arm64 still
// leaks `dyn` (no __drop_dyn helper, slice 4c), so it must not be swept
// there or the dec would call a missing fn; b.dynReclaim gates that.
func (b *builder) rcSlotTracked(t ast.Type) bool {
	if rcZeroInitClass(t) {
		return true
	}
	if _, isDyn := t.(ast.DynTraitType); isDyn && b.dynReclaim() {
		return true
	}
	return false
}

// rcStringReclaimable reports whether a string participates in per-element /
// per-field / capture drops on a backend with pointer width ptrW: two-word
// string ABIs (wasm + arm64-TwoWordOverride) reach __fern_str_dec, and
// native single-word (x86_64) reaches __fern_rc_dec. This is the one
// definition of the condition tupleNeedsDrop and hasRcCapture used to spell
// out longhand as two branches each.
func rcStringReclaimable(ptrW int) bool {
	return ast.UseTwoWordStrings(ptrW) || ptrW == 8
}

// arrElemIsRcTracked is the legacy name for the PtrShaped axis — kept as a
// view because it reads naturally at array-element / capture / payload call
// sites. Strings are deliberately excluded: they are never inc'd on array
// insertion and must not be dec'd by the element loop.
func arrElemIsRcTracked(elem ast.Type) bool {
	return rcPtrShaped(elem)
}

// ---------------------------------------------------------------------------
// Transitive axes — one walk, both facts
// ---------------------------------------------------------------------------

// rcTypeFacts is the single type-graph walk behind the transitive axes: it
// returns whether `t`'s deep drop is string/array-free AND whether `t`
// transitively contains a Map, in one pass. `seen` breaks recursive-type
// cycles (a self-recursive enum like List is fine: the back-edge is assumed
// clean on both axes, and any violation on a real payload is caught on its
// own first visit before the back-edge is taken). Conservative defaults
// preserved from the two predecessor walkers:
//   - unknown struct decl: not string/array-free; no Map (runtime handles —
//     Reader / Writer / MapIter — have no decl and land here);
//   - unknown enum decl (generic-erased): not free AND Map-containing, the
//     worst verdict on both axes;
//   - Map itself: not free, contains Map;
//   - array / slice: never free, and the Map fact recurses into the element;
//   - anything else (ParamType, closures, dyn, …): not free, no Map.
func (b *builder) rcTypeFacts(t ast.Type, seen map[string]bool) (stringArrayFree, containsMap bool) {
	switch ty := t.(type) {
	case ast.NumberType, ast.BoolType, ast.FloatType, ast.VoidType:
		return true, false
	case ast.StringType:
		return false, false
	case ast.ArrayType:
		_, m := b.rcTypeFacts(ty.Elem, seen)
		return false, m
	case ast.SliceType:
		_, m := b.rcTypeFacts(ty.Elem, seen)
		return false, m
	case ast.TupleType:
		free, m := true, false
		for _, e := range ty.Elems {
			ef, em := b.rcTypeFacts(e, seen)
			free = free && ef
			m = m || em
		}
		return free, m
	case ast.StructType:
		if ty.Name == "Map" {
			return false, true
		}
		if seen["s:"+ty.Name] {
			return true, false
		}
		seen["s:"+ty.Name] = true
		sd, ok := b.info.Structs[ty.Name]
		if !ok {
			return false, false
		}
		free, m := true, false
		for _, f := range sd.Fields {
			ff, fm := b.rcTypeFacts(f.Type, seen)
			free = free && ff
			m = m || fm
		}
		return free, m
	case ast.EnumType:
		if seen["e:"+ty.Name] {
			return true, false
		}
		seen["e:"+ty.Name] = true
		ed, ok := b.info.Enums[ty.Name]
		if !ok {
			return false, true
		}
		free, m := true, false
		for _, v := range ed.Variants {
			for _, pl := range v.Payloads {
				pf, pm := b.rcTypeFacts(pl, seen)
				free = free && pf
				m = m || pm
			}
		}
		return free, m
	}
	return false, false
}

// typeIsStringArrayFree is the StringArrayFree view: `t`'s deep-drop
// reclaims no string or array buffer — i.e. t is built transitively from
// scalars, enums, structs, and tuples only.
func (b *builder) typeIsStringArrayFree(t ast.Type) bool {
	free, _ := b.rcTypeFacts(t, map[string]bool{})
	return free
}

// enumContainsMap is the ContainsMap view keyed by enum name (the walk
// resolves the decl from b.info, so type args are irrelevant — same as the
// predecessor enumTransitivelyContainsMap).
func (b *builder) enumContainsMap(enumName string) bool {
	_, m := b.rcTypeFacts(ast.EnumType{Name: enumName}, map[string]bool{})
	return m
}

// ---------------------------------------------------------------------------
// Derived verdicts
// ---------------------------------------------------------------------------

// enumRcPayloadsEligible reports whether the Slice-1b EnumRcPayloads model
// applies to enum `enumName`: the flag is on AND the enum's deep drop is
// fully wired on every backend. Enums whose payloads transitively contain a
// Map are excluded — see rcCaps.ContainsMap. Excluded enums keep the move
// model (flag-off behaviour) at every site, a documented safe leak.
func (b *builder) enumRcPayloadsEligible(enumName string) bool {
	return ast.EnumRcPayloads && !b.enumContainsMap(enumName)
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
		en, _, _, isVar := b.lookupVariant(id.Name)
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

// rcPreciseDroppable is the PreciseDroppable verdict; `containsMap` is the
// walk fact for t (passed in so rcCapsOf computes the walk once).
//
// Arrays: emitOwnedSlotDrop reclaims every element kind fully — primitive
// via `__fern_arr_dec` (pure buffer free, slice 1); rc-tracked (`struct[]` /
// `enum[]` / `T[][]` / `tuple[]`) via the deep `__drop_arr_*` loop (slice
// 2); and `string[]` via `__fern_drop_arr_str` / `__fern_drop_arr_ptr`
// (slice 3 — str_dec each element, then the buffer). Each per-element drop
// is_unique-gates, so a counted alias of an element only DECs.
//
// Struct / Map / tuple boxes (slice 4): construction INCs their pointer
// fields/elements (StructLit / TupleLit), so a precise drop is rc-protected
// — the same reason slice-2 rc-element arrays are sound. Non-droppable
// runtime handles (Reader / Writer / MapIter) aren't freeEligible, so they
// never reach here.
//
// Enums: precise-droppable only under Slice 1b (rc-eligible enums) — once
// enum construction rc-counts its pointer payloads the deep drop is
// rc-protected exactly like a struct. Under the default move model (or for
// Map-containing enums) they stay excluded (payloads carry no counted box
// reference).
func (b *builder) rcPreciseDroppable(t ast.Type, containsMap bool) bool {
	if _, isEnum := t.(ast.EnumType); isEnum {
		return ast.EnumRcPayloads && !containsMap
	}
	switch t.(type) {
	case ast.ArrayType, ast.StructType, ast.TupleType:
		return true
	}
	return false
}

// rcOwnedByDefault is the OwnedByDefault verdict; `stringArrayFree` and
// `containsMap` are the walk facts for t. Rolled out per category, enums
// first: they're immutable (so the caller-side retain inc can't disturb the
// in-place mutation the borrow model protects) and, post-1b, their boxes are
// rc-counted and deep-droppable.
func (b *builder) rcOwnedByDefault(t ast.Type, stringArrayFree, containsMap bool) bool {
	if !ast.OwnedByDefault {
		return false
	}
	switch ty := t.(type) {
	case ast.EnumType:
		// Enums (sub-slice 2a): only those whose deep drop is a pure,
		// fully-wired box/enum walk — transitively scalar/enum/tuple payloads
		// (no array / string / Map, keeping the array-payload deep-drop +
		// self-overwrite-reuse interaction out of scope, e.g. `enum Bag {
		// Keep(i32[]) }`) AND a UNIFORM box layout (emitDec only frees a
		// uniform enum; a non-uniform one like `Shape { Circle(i32),
		// Rect(i32,i32) }` flat-decs without freeing, so owned-by-default
		// would mis-reclaim it). That is the FBIP list/tree case; other enums
		// keep the borrow model for now.
		if !ast.EnumRcPayloads || containsMap || !stringArrayFree {
			return false
		}
		ed, ok := b.info.Enums[ty.Name]
		if !ok {
			return false
		}
		_, uniform := uniformEnumBoxSize(ed, b.ptrW)
		return uniform
	case ast.StructType:
		// Structs (sub-slice 2c): Fern struct fields are immutable after
		// construction (the checker rejects `p.x = v`; the idiom is a
		// whole-struct rebuild `p = P{...old, x: v}`), so — exactly like
		// enums — the caller-side retain inc that owned-by-default adds can
		// never disturb an in-place mutation through the parameter; there is
		// none. Admit a struct whose deep drop is the fully-wired pointer-box
		// walk: transitively string/array/slice/Map-free (so
		// __drop_struct_<N> never hits an unwired field) and backed by a real
		// StructDecl. Runtime handles (Map / Reader / Writer / MapIter) have
		// no decl and read not-free anyway; the box is uniform by
		// construction (no variants), so no uniformity check is needed.
		// Per-field rc counting (Phase 1e-struct-ii) balances the drop.
		if _, ok := b.info.Structs[ty.Name]; !ok {
			return false
		}
		return stringArrayFree
	case ast.TupleType:
		// Tuples (sub-slice 2c): immutable, uniform headered boxes whose
		// elements are rc-counted (the projection-site dup balances the
		// per-element drop in emitDec's tuple branch). Same
		// string/array/slice/Map-free gate as structs keeps the deep drop on
		// the fully-wired path.
		return stringArrayFree
	}
	return false
}

// isOwnedByDefaultType is the OwnedByDefault view: a parameter of type `t`
// is owned by the callee under Slice 2 (OwnedByDefault). The callee reclaims
// such a parameter at exit; the caller retains it with an inc at the call
// site. See rcOwnedByDefault for the per-category rationale.
func (b *builder) isOwnedByDefaultType(t ast.Type) bool {
	if !ast.OwnedByDefault {
		return false
	}
	switch t.(type) {
	case ast.EnumType, ast.StructType, ast.TupleType:
		free, hasMap := b.rcTypeFacts(t, map[string]bool{})
		return b.rcOwnedByDefault(t, free, hasMap)
	}
	return false
}

// preciseDroppableType is the PreciseDroppable view keyed by local name:
// whether `name`'s declared type is in the precise-drop scope. freeEligible
// (the taint set) excludes any value whose nested fields/payloads alias a
// live local, and the init/use alias gates exclude boxes bound from /
// flowing into an uncounted alias — this predicate is only the TYPE half of
// that soundness argument (see rcPreciseDroppable).
func (b *builder) preciseDroppableType(name string) bool {
	t, ok := b.localDeclType(name)
	if !ok {
		return false
	}
	if et, isEnum := t.(ast.EnumType); isEnum {
		// Match enumRcPayloadsEligible's short-circuit: no walk when the
		// EnumRcPayloads flag is off.
		return ast.EnumRcPayloads && b.rcPreciseDroppable(t, b.enumContainsMap(et.Name))
	}
	return b.rcPreciseDroppable(t, false)
}

// ---------------------------------------------------------------------------
// Decl-level capabilities (drop-fn routing)
// ---------------------------------------------------------------------------

// tupleNeedsDrop reports whether tt has at least one element worth
// recursing through — its drop fn dec's only rc-tracked / string
// elements, so a tuple of plain i32s (or any other non-rc shape) has
// nothing to do beyond the surrounding box dec the caller already
// emits. Mirrors enumNeedsDrop in role: dropFnNameFor uses it to
// decide whether to register and route through `__drop_tuple_<...>`
// at all.
func tupleNeedsDrop(tt ast.TupleType, ptrW int) bool {
	for _, et := range tt.Elems {
		if rcPtrShaped(et) {
			return true
		}
		if _, isStr := et.(ast.StringType); isStr && rcStringReclaimable(ptrW) {
			return true
		}
	}
	return false
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
// offset). It's also the gate for adopting a generic instantiation's
// substituted decl — a pointer payload proves a heap-boxed (non-pair-
// form) instantiation, so the variant-plan's box_free is valid; scalar
// payloads (pair-form, no box) read false and stay on the flat path.
func enumHasPointerPayload(ed *ast.EnumDecl) bool {
	for _, v := range ed.Variants {
		for _, pt := range v.Payloads {
			if rcPtrShaped(pt) {
				return true
			}
		}
	}
	return false
}
