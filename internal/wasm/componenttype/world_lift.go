package componenttype

// world_lift.go is P2 slice 1 of bring-your-own WIT
// (docs/WIT-BRING-YOUR-OWN.md): lift the raw decoded World (the type-tree P1
// produced) into a queryable per-interface model. P1 proved we can decode and
// round-trip the component-type binary; P2 starts using it — and the first
// thing the import classifier and type-import emitter both need is, per
// imported interface, its exported functions and resources. Later P2 slices
// resolve full signatures and re-emit the type-import sections.

// WorldInterface is one imported WASI interface of a world: its versioned
// name plus the functions and resources it exports.
type WorldInterface struct {
	Name      string
	Funcs     []string
	Resources []string

	// FuncSigs are the exported functions with resolved signatures (P2 slice
	// 2), parallel in order to Funcs. LocalTypes is the interface's
	// type-index space: a function param/result Valtype with IsPrim == false
	// resolves to LocalTypes[Idx] (nil for a slot introduced by a type alias —
	// i.e. an imported resource handle, which lowers as a scalar).
	FuncSigs   []WorldFunc
	LocalTypes []*TypeDef
}

// WorldFunc is an exported function with its decoded signature. Resolve a
// param/result type index against the owning interface's LocalTypes.
type WorldFunc struct {
	Name string
	Sig  *FuncType
}

// Interfaces lifts the world's import declarations into per-interface
// function/resource inventories, in import order. It resolves each import's
// instance type through the world component's type-index space (which grows
// on both `type` decls and `type` aliases).
func (w *World) Interfaces() []WorldInterface {
	world := w.worldComponent()
	if world == nil {
		return nil
	}
	// localTypes mirrors the world component's type-index space; entries are
	// nil for slots introduced by a type alias (we only need to resolve
	// import targets, which point at instance `type` decls).
	var localTypes []*TypeDef
	var out []WorldInterface
	for i := range world.Decls {
		d := &world.Decls[i]
		switch d.Kind {
		case 0x01: // type
			localTypes = append(localTypes, d.Type)
		case 0x02: // alias
			if d.Alias != nil && d.Alias.Sort == 0x03 { // type alias occupies a type slot
				localTypes = append(localTypes, nil)
			}
		case 0x03: // import
			if d.Extern == nil || d.Extern.Kind != 0x05 { // 0x05 = instance
				continue
			}
			idx := int(d.Extern.TypeIdx)
			if idx < 0 || idx >= len(localTypes) || localTypes[idx] == nil {
				continue
			}
			out = append(out, liftInterface(d.Name, localTypes[idx]))
		}
	}
	return out
}

// worldComponent returns the inner world component type — the first `type`
// decl of the outer wrapper component whose type is itself a component.
func (w *World) worldComponent() *TypeDef {
	if len(w.Types) == 0 || w.Types[0].Tag != tagComponent {
		return nil
	}
	for i := range w.Types[0].Decls {
		d := &w.Types[0].Decls[i]
		if d.Kind == 0x01 && d.Type != nil && d.Type.Tag == tagComponent {
			return d.Type
		}
	}
	return nil
}

// liftInterface collects an interface instance type's exported functions
// (externdesc func) and resources (externdesc type with a `sub` bound), and
// resolves each function's signature against the interface's local type-index
// space.
func liftInterface(name string, inst *TypeDef) WorldInterface {
	wi := WorldInterface{Name: name}
	// Build the instance's type-index space. Anything that binds a type index
	// advances it: a `type` decl (its defined type), a `type` alias, and an
	// import/export whose externdesc is a type — the latter covers resource
	// exports (`export X: (type (sub resource))`) and type re-exports, which
	// we only need as nil slots to keep the indices aligned so a function's
	// externdesc typeidx lands on the right func type.
	for i := range inst.Decls {
		d := &inst.Decls[i]
		switch {
		case d.Kind == 0x01:
			wi.LocalTypes = append(wi.LocalTypes, d.Type)
		case d.Kind == 0x02 && d.Alias != nil && d.Alias.Sort == 0x03:
			wi.LocalTypes = append(wi.LocalTypes, nil)
		case (d.Kind == 0x03 || d.Kind == 0x04) && d.Extern != nil && d.Extern.Kind == 0x03:
			// A type externdesc binds an index. `(type (eq N))` re-exports an
			// earlier type — e.g. `export "datetime" (type (eq 0))` aliases the
			// record at index 0 — so resolve the slot to that target def. A
			// function whose param/result references this slot must then see the
			// underlying type (the datetime record is a 2-field KindMem result,
			// not an opaque handle); leaving it nil mis-lowers `now` as a no-opt
			// and drops the required `memory` canonical option. A
			// `(type (sub resource))` bound is a genuine opaque handle → nil.
			if d.Extern.Bound == 0x00 && int(d.Extern.BoundIdx) < len(wi.LocalTypes) {
				wi.LocalTypes = append(wi.LocalTypes, wi.LocalTypes[d.Extern.BoundIdx])
			} else {
				wi.LocalTypes = append(wi.LocalTypes, nil)
			}
		}
	}
	for i := range inst.Decls {
		d := &inst.Decls[i]
		if d.Kind != 0x04 || d.Extern == nil { // export
			continue
		}
		switch {
		case d.Extern.Kind == 0x01: // func
			wi.Funcs = append(wi.Funcs, d.Name)
			var sig *FuncType
			if idx := int(d.Extern.TypeIdx); idx >= 0 && idx < len(wi.LocalTypes) &&
				wi.LocalTypes[idx] != nil && wi.LocalTypes[idx].Tag == tagFunc {
				sig = wi.LocalTypes[idx].Func
			}
			wi.FuncSigs = append(wi.FuncSigs, WorldFunc{Name: d.Name, Sig: sig})
		case d.Extern.Kind == 0x03 && d.Extern.Bound == 0x01: // type (sub) = resource
			wi.Resources = append(wi.Resources, d.Name)
		}
	}
	return wi
}

// ExportedInterfaces lifts the world's *export* declarations into per-interface
// function/resource inventories (mirroring Interfaces for imports), in
// declaration order. A world export is an instance the component must provide
// (e.g. `export wasi:cli/run@0.2.0` / a custom `local:test/math@0.1.0`); P6
// (docs/WIT-BRING-YOUR-OWN.md) lifts a Fern `@export` function as one of these.
func (w *World) ExportedInterfaces() []WorldInterface {
	world := w.worldComponent()
	if world == nil {
		return nil
	}
	var localTypes []*TypeDef
	var out []WorldInterface
	for i := range world.Decls {
		d := &world.Decls[i]
		switch d.Kind {
		case 0x01: // type
			localTypes = append(localTypes, d.Type)
		case 0x02: // alias
			if d.Alias != nil && d.Alias.Sort == 0x03 {
				localTypes = append(localTypes, nil)
			}
		case 0x04: // export
			if d.Extern == nil || d.Extern.Kind != 0x05 { // 0x05 = instance
				continue
			}
			idx := int(d.Extern.TypeIdx)
			if idx < 0 || idx >= len(localTypes) || localTypes[idx] == nil {
				continue
			}
			out = append(out, liftInterface(d.Name, localTypes[idx]))
		}
	}
	return out
}

// ExportFunc finds the signature of an exported function `name` on the world's
// exported interface `iface` (P6). Returns (sig, true) when the world declares
// it; sig may be nil if the interface exports the name but its type didn't
// resolve to a func type.
func (w *World) ExportFunc(iface, name string) (*FuncType, bool) {
	for _, wi := range w.ExportedInterfaces() {
		if wi.Name != iface {
			continue
		}
		for _, f := range wi.FuncSigs {
			if f.Name == name {
				return f.Sig, true
			}
		}
	}
	return nil, false
}

// ResolveDef returns the defined type a value type refers to within this
// interface, or nil for a primitive or an unresolved (handle) slot.
func (wi WorldInterface) ResolveDef(v Valtype) *DefinedType {
	if v.IsPrim || int(v.Idx) >= len(wi.LocalTypes) {
		return nil
	}
	td := wi.LocalTypes[v.Idx]
	if td == nil {
		return nil
	}
	return td.Def // nil unless td is a defvaltype
}

// ListElemPrim reports, when `v` resolves to a `list<P>` whose element `P` is a
// primitive, that primitive's CValtype byte (the single-byte code, which is
// also the component-model valtype byte). Returns (0, false) otherwise. The P6
// export lift (docs/WIT-BRING-YOUR-OWN.md) uses it to build the `list<T>`
// component type for a numeric-array export result without exposing the
// internal tag constants.
func (wi WorldInterface) ListElemPrim(v Valtype) (byte, bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagList || !d.Elem.IsPrim {
		return 0, false
	}
	return d.Elem.Prim, true
}

// OptionElemPrim reports, when `v` resolves to an `option<P>` whose element is a
// primitive, that primitive's CValtype byte. The P6 export lift uses it to emit
// the `option` component type for an Option export result without exposing the
// internal tag constants.
func (wi WorldInterface) OptionElemPrim(v Valtype) (byte, bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagOption || !d.Elem.IsPrim {
		return 0, false
	}
	return d.Elem.Prim, true
}

// ResultArmPrims reports, when `v` resolves to a `result<ok, err>` with both
// arms present and primitive, the two arms' CValtype bytes. Returns (0,0,false)
// otherwise. The P6 export lift uses it to emit the `result` component type for
// a Result export result; Fern's Result[T,E] always carries both arms.
func (wi WorldInterface) ResultArmPrims(v Valtype) (ok byte, err byte, both bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagResult || !d.HasOk || !d.HasErr || !d.Ok.IsPrim || !d.Err.IsPrim {
		return 0, 0, false
	}
	return d.Ok.Prim, d.Err.Prim, true
}
