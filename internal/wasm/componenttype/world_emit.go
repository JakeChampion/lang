package componenttype

import "fmt"

// world_emit.go is the start of P2's emission (docs/WIT-BRING-YOUR-OWN.md,
// "P2 finding & direction"): turn a decoded world interface back into the
// component **type + import** sections that declare it as a top-level import —
// the *full* interface, not a minimized subset — gated by the result
// validating/running under the WASI tools rather than by byte-identity.
//
// First slice: a self-contained interface — one whose instance type does not
// reference shared types from other interfaces via outer aliases (e.g.
// wasi:io/error, which only declares the `error` resource). Cross-interface
// surfacing (io/streams aliasing io/error's `error`, etc.) is the next slice.

const (
	compSecType   = 0x07
	compSecImport = 0x0a // 10
)

// instanceTypeFor returns the instance type the world imports under `name`,
// resolved through the world component's type-index space.
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

// referencesSharedTypes reports whether an interface instance type pulls in a
// type from an enclosing scope (an outer alias) — i.e. a shared type that must
// be surfaced separately before this interface can be emitted standalone.
func (td *TypeDef) referencesSharedTypes() bool {
	for i := range td.Decls {
		d := &td.Decls[i]
		if d.Kind == 0x02 && d.Alias != nil && d.Alias.Target == 0x02 { // outer
			return true
		}
	}
	return false
}

// EmitInterfaceTypeImport emits the component type + import sections that
// declare `name` as a top-level import of its full interface. The interface
// must be self-contained (no outer-aliased shared types); otherwise an error
// is returned until the surfacing slice lands.
func (w *World) EmitInterfaceTypeImport(name string) ([]byte, error) {
	inst := w.instanceTypeFor(name)
	if inst == nil {
		return nil, fmt.Errorf("componenttype: interface %q not imported by world", name)
	}
	if inst.referencesSharedTypes() {
		return nil, fmt.Errorf("componenttype: interface %q references shared types via outer alias; cross-interface surfacing not implemented yet", name)
	}
	// type section: vec(1) defining the instance type as component type 0.
	tbody := appendULEB(nil, 1)
	tbody = encodeTypeDef(tbody, *inst)
	out := wrapCompSection(nil, compSecType, tbody)
	// import section: vec(1), label name, instance externdesc referencing type 0.
	ibody := appendULEB(nil, 1)
	ibody = append(ibody, 0x00) // importname kind = label
	ibody = appendName(ibody, name)
	ibody = append(ibody, 0x05) // externdesc kind = instance
	ibody = appendULEB(ibody, 0)
	return wrapCompSection(out, compSecImport, ibody), nil
}

// ComponentWithImport builds a complete (import-only) component declaring the
// named interface as a full top-level import — for validating the emission
// with the WASI tools.
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
