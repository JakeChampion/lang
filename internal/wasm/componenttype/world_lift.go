package componenttype

type WorldInterface struct {
	Name      string
	Funcs     []string
	Resources []string

	FuncSigs        []WorldFunc
	LocalTypes      []*TypeDef
	LocalTypeNames  []string
	LocalTypeOwners []*WorldInterface
}

func (wi *WorldInterface) ownerOf(idx uint32) *WorldInterface {
	if int(idx) < len(wi.LocalTypeOwners) && wi.LocalTypeOwners[idx] != nil {
		return wi.LocalTypeOwners[idx]
	}
	return wi
}

type WorldFunc struct {
	Name string
	Sig  *FuncType
}

func (w *World) Interfaces() []WorldInterface {
	world := w.worldComponent()
	if world == nil {
		return nil
	}
	var localTypes []*TypeDef
	var slotAliases []*Alias
	var instances []string
	lifted := map[string]*WorldInterface{}
	var out []WorldInterface
	resolve := func(idx uint32) (*TypeDef, *WorldInterface) {
		if int(idx) >= len(slotAliases) || slotAliases[idx] == nil || slotAliases[idx].Target != 0x00 {
			return nil, nil
		}
		a := slotAliases[idx]
		if int(a.InstIdx) >= len(instances) {
			return nil, nil
		}
		owner, ok := lifted[instances[a.InstIdx]]
		if !ok {
			return nil, nil
		}
		for j, n := range owner.LocalTypeNames {
			if n == a.Name && owner.LocalTypes[j] != nil {
				return owner.LocalTypes[j], owner.ownerOf(uint32(j))
			}
		}
		return nil, nil
	}
	for i := range world.Decls {
		d := &world.Decls[i]
		switch d.Kind {
		case 0x01:
			localTypes = append(localTypes, d.Type)
			slotAliases = append(slotAliases, nil)
		case 0x02:
			if d.Alias != nil && d.Alias.Sort == 0x03 {
				localTypes = append(localTypes, nil)
				slotAliases = append(slotAliases, d.Alias)
			}
		case 0x03:
			if d.Extern == nil || d.Extern.Kind != 0x05 {
				continue
			}
			instances = append(instances, d.Name)
			idx := int(d.Extern.TypeIdx)
			if idx < 0 || idx >= len(localTypes) || localTypes[idx] == nil {
				continue
			}
			wi := liftInterface(d.Name, localTypes[idx], resolve)
			lifted[d.Name] = &wi
			out = append(out, wi)
		}
	}
	return out
}

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

func liftInterface(name string, inst *TypeDef, resolve func(idx uint32) (*TypeDef, *WorldInterface)) WorldInterface {
	wi := WorldInterface{Name: name}
	for i := range inst.Decls {
		d := &inst.Decls[i]
		switch {
		case d.Kind == 0x01:
			wi.LocalTypes = append(wi.LocalTypes, d.Type)
			wi.LocalTypeNames = append(wi.LocalTypeNames, "")
			wi.LocalTypeOwners = append(wi.LocalTypeOwners, nil)
		case d.Kind == 0x02 && d.Alias != nil && d.Alias.Sort == 0x03:
			var td *TypeDef
			var owner *WorldInterface
			if resolve != nil && d.Alias.Target == 0x02 && d.Alias.Count == 1 {
				td, owner = resolve(d.Alias.Index)
			}
			wi.LocalTypes = append(wi.LocalTypes, td)
			wi.LocalTypeNames = append(wi.LocalTypeNames, "")
			wi.LocalTypeOwners = append(wi.LocalTypeOwners, owner)
		case (d.Kind == 0x03 || d.Kind == 0x04) && d.Extern != nil && d.Extern.Kind == 0x03:
			wi.LocalTypeNames = append(wi.LocalTypeNames, d.Name)
			if d.Extern.Bound == 0x00 && int(d.Extern.BoundIdx) < len(wi.LocalTypes) {
				wi.LocalTypes = append(wi.LocalTypes, wi.LocalTypes[d.Extern.BoundIdx])
				wi.LocalTypeOwners = append(wi.LocalTypeOwners, wi.LocalTypeOwners[d.Extern.BoundIdx])
			} else {
				wi.LocalTypes = append(wi.LocalTypes, nil)
				wi.LocalTypeOwners = append(wi.LocalTypeOwners, nil)
			}
		}
	}
	for i := range inst.Decls {
		d := &inst.Decls[i]
		if d.Kind != 0x04 || d.Extern == nil {
			continue
		}
		switch {
		case d.Extern.Kind == 0x01:
			wi.Funcs = append(wi.Funcs, d.Name)
			var sig *FuncType
			if idx := int(d.Extern.TypeIdx); idx >= 0 && idx < len(wi.LocalTypes) &&
				wi.LocalTypes[idx] != nil && wi.LocalTypes[idx].Tag == tagFunc {
				sig = wi.LocalTypes[idx].Func
			}
			wi.FuncSigs = append(wi.FuncSigs, WorldFunc{Name: d.Name, Sig: sig})
		case d.Extern.Kind == 0x03 && d.Extern.Bound == 0x01:
			wi.Resources = append(wi.Resources, d.Name)
		}
	}
	return wi
}

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
		case 0x01:
			localTypes = append(localTypes, d.Type)
		case 0x02:
			if d.Alias != nil && d.Alias.Sort == 0x03 {
				localTypes = append(localTypes, nil)
			}
		case 0x04:
			if d.Extern == nil || d.Extern.Kind != 0x05 {
				continue
			}
			idx := int(d.Extern.TypeIdx)
			if idx < 0 || idx >= len(localTypes) || localTypes[idx] == nil {
				continue
			}
			out = append(out, liftInterface(d.Name, localTypes[idx], nil))
		}
	}
	return out
}

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

func (wi WorldInterface) ResolveDef(v Valtype) *DefinedType {
	if v.IsPrim || int(v.Idx) >= len(wi.LocalTypes) {
		return nil
	}
	td := wi.LocalTypes[v.Idx]
	if td == nil {
		return nil
	}
	return td.Def
}

func (wi WorldInterface) HandleResource(v Valtype) (name string, owned bool, ok bool) {
	d := wi.ResolveDef(v)
	if d == nil || (d.Tag != tagOwn && d.Tag != tagBorrow) {
		return "", false, false
	}
	if int(d.Resource) >= len(wi.LocalTypeNames) {
		return "", false, false
	}
	name = wi.LocalTypeNames[d.Resource]
	if name == "" {
		return "", false, false
	}
	return name, d.Tag == tagOwn, true
}

func (wi WorldInterface) ListElemPrim(v Valtype) (byte, bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagList || !d.Elem.IsPrim {
		return 0, false
	}
	return d.Elem.Prim, true
}

func (wi WorldInterface) OptionElemPrim(v Valtype) (byte, bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagOption || !d.Elem.IsPrim {
		return 0, false
	}
	return d.Elem.Prim, true
}

func (wi WorldInterface) ResultArmPrims(v Valtype) (ok byte, err byte, both bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagResult || !d.HasOk || !d.HasErr || !d.Ok.IsPrim || !d.Err.IsPrim {
		return 0, 0, false
	}
	return d.Ok.Prim, d.Err.Prim, true
}

func (wi WorldInterface) TupleElemPrims(v Valtype) ([]byte, bool) {
	d := wi.ResolveDef(v)
	if d == nil || d.Tag != tagTuple || len(d.Elems) == 0 {
		return nil, false
	}
	out := make([]byte, len(d.Elems))
	for i, e := range d.Elems {
		if !e.IsPrim {
			return nil, false
		}
		out[i] = e.Prim
	}
	return out, true
}
