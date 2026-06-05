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
// (externdesc func) and resources (externdesc type with a `sub` bound).
func liftInterface(name string, inst *TypeDef) WorldInterface {
	wi := WorldInterface{Name: name}
	for i := range inst.Decls {
		d := &inst.Decls[i]
		if d.Kind != 0x04 || d.Extern == nil { // export
			continue
		}
		switch {
		case d.Extern.Kind == 0x01: // func
			wi.Funcs = append(wi.Funcs, d.Name)
		case d.Extern.Kind == 0x03 && d.Extern.Bound == 0x01: // type (sub) = resource
			wi.Resources = append(wi.Resources, d.Name)
		}
	}
	return wi
}
