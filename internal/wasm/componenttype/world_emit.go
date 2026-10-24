package componenttype

import "fmt"

const (
	compSecType   = 0x07
	compSecImport = 0x0a
)

func (w *World) instanceTypeFor(name string) *TypeDef {
	world := w.worldComponent()
	if world == nil {
		return nil
	}
	var localTypes []*TypeDef
	for i := range world.Decls {
		d := &world.Decls[i]
		switch {
		case d.Kind == 0x01:
			localTypes = append(localTypes, d.Type)
		case d.Kind == 0x02 && d.Alias != nil && d.Alias.Sort == 0x03:
			localTypes = append(localTypes, nil)
		case (d.Kind == 0x03 || d.Kind == 0x04) && d.Extern != nil && d.Extern.Kind == 0x03:
			localTypes = append(localTypes, nil)
		case d.Kind == 0x03 && d.Extern != nil && d.Extern.Kind == 0x05 && d.Name == name:
			if idx := int(d.Extern.TypeIdx); idx >= 0 && idx < len(localTypes) {
				return localTypes[idx]
			}
		}
	}
	return nil
}

func (td *TypeDef) referencesSharedTypes() bool {
	for i := range td.Decls {
		d := &td.Decls[i]
		if d.Kind == 0x02 && d.Alias != nil && d.Alias.Target == 0x02 {
			return true
		}
	}
	return false
}

func (w *World) EmitInterfaceTypeImport(name string) ([]byte, error) {
	inst := w.instanceTypeFor(name)
	if inst == nil {
		return nil, fmt.Errorf("componenttype: interface %q not imported by world", name)
	}
	if inst.referencesSharedTypes() {
		return nil, fmt.Errorf("componenttype: interface %q references shared types via outer alias; cross-interface surfacing not implemented yet", name)
	}
	tbody := appendULEB(nil, 1)
	tbody = encodeTypeDef(tbody, *inst)
	out := wrapCompSection(nil, compSecType, tbody)
	ibody := appendULEB(nil, 1)
	ibody = append(ibody, 0x00)
	ibody = appendName(ibody, name)
	ibody = append(ibody, 0x05)
	ibody = appendULEB(ibody, 0)
	return wrapCompSection(out, compSecImport, ibody), nil
}

func (w *World) ComponentWithImport(name string) ([]byte, error) {
	secs, err := w.EmitInterfaceTypeImport(name)
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, componentHeader...)
	return append(out, secs...), nil
}

func wrapCompSection(out []byte, id byte, body []byte) []byte {
	out = append(out, id)
	out = appendULEB(out, uint64(len(body)))
	return append(out, body...)
}

const compSecAlias = 0x06

func (w *World) EmitWorldImports() ([]byte, error) {
	world := w.worldComponent()
	if world == nil {
		return nil, fmt.Errorf("componenttype: no world component")
	}
	var out []byte
	for i := range world.Decls {
		d := &world.Decls[i]
		switch d.Kind {
		case 0x01:
			body := appendULEB(nil, 1)
			body = encodeTypeDef(body, *d.Type)
			out = wrapCompSection(out, compSecType, body)
		case 0x02:
			body := appendULEB(nil, 1)
			body = encodeAlias(body, *d.Alias)
			out = wrapCompSection(out, compSecAlias, body)
		case 0x03:
			body := appendULEB(nil, 1)
			body = append(body, 0x00)
			body = appendName(body, d.Name)
			body = encodeExternDesc(body, *d.Extern)
			out = wrapCompSection(out, compSecImport, body)
		case 0x04:
			if d.Extern != nil && d.Extern.Kind == 0x03 {
				return nil, fmt.Errorf("componenttype: world type-export not supported in import prefix yet")
			}
		default:
			return nil, fmt.Errorf("componenttype: world decl kind %#x not supported in import prefix", d.Kind)
		}
	}
	return out, nil
}

func (w *World) ComponentWithWorldImports() ([]byte, error) {
	secs, err := w.EmitWorldImports()
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, componentHeader...)
	return append(out, secs...), nil
}

type PrefixLayout struct {
	Types     uint32
	Instances uint32
}

func (w *World) PrefixLayout() PrefixLayout {
	world := w.worldComponent()
	var pl PrefixLayout
	if world == nil {
		return pl
	}
	for i := range world.Decls {
		d := &world.Decls[i]
		switch {
		case d.Kind == 0x01:
			pl.Types++
		case d.Kind == 0x02 && d.Alias != nil && d.Alias.Sort == 0x03:
			pl.Types++
		case (d.Kind == 0x03 || d.Kind == 0x04) && d.Extern != nil && d.Extern.Kind == 0x03:
			pl.Types++
		}
		if d.Kind == 0x03 {
			pl.Instances++
		}
	}
	return pl
}

func (w *World) ImportInstanceIndex(name string) int {
	world := w.worldComponent()
	if world == nil {
		return -1
	}
	idx := 0
	for i := range world.Decls {
		d := &world.Decls[i]
		if d.Kind == 0x03 {
			if d.Name == name {
				return idx
			}
			idx++
		}
	}
	return -1
}
