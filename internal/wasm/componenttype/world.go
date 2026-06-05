package componenttype

import "fmt"

// world.go is P1 slice 3 of the bring-your-own-WIT decoder
// (docs/WIT-BRING-YOUR-OWN.md): the component type-section entry reader. It
// wraps slice 2's value-type grammar into the full nested type model —
// component / instance types and their declarations (type / alias / import /
// export), externdescs, and aliases — and decodes a whole `component-type`
// payload into a structured World that re-encodes byte-for-byte.
//
// Encoding tags (reverse-engineered against fern.bin alongside `wasm-tools
// dump`):
//
//	deftype:   0x41 component | 0x42 instance | 0x40 func | 0x68..0x72
//	           defvaltype | 0x73..0x7f primitive
//	decl:      0x00 core-type | 0x01 type | 0x02 alias | 0x03 import |
//	           0x04 export
//	name:      0x00 (label) + uleb-len + bytes
//	externdesc:0x01 func <typeidx> | 0x02 value <valtype> |
//	           0x03 type (0x00 eq <typeidx> | 0x01 sub) |
//	           0x04 component <typeidx> | 0x05 instance <typeidx>
//	alias:     <sort> <target> <operands>; sort 0x00 core adds a core-sort
//	           byte; target 0x00 instance-export (<instidx> <name>),
//	           0x01 core-instance-export (<instidx> <name>),
//	           0x02 outer (<count> <index>)

const (
	tagComponent = 0x41
	tagInstance  = 0x42
)

// TypeDef is one component-model type: a component/instance type (a list of
// declarations), a function type, a defined value type, or a primitive.
type TypeDef struct {
	Tag   byte
	Decls []Decl       // component (0x41) / instance (0x42)
	Func  *FuncType    // func (0x40)
	Def   *DefinedType // defvaltype (0x68..0x72)
	// primitive: Tag holds the primitive code (0x73..0x7f)
}

// Decl is one declaration inside a component/instance type.
type Decl struct {
	Kind byte // 0x00 core-type, 0x01 type, 0x02 alias, 0x03 import, 0x04 export

	Type    *TypeDef    // Kind 0x01
	Alias   *Alias      // Kind 0x02
	Name    string      // Kind 0x03 / 0x04 (import/export name)
	Extern  *ExternDesc // Kind 0x03 / 0x04
	RawCore []byte      // Kind 0x00: opaque core-type body (none in the shipped worlds)
}

// ExternDesc describes the type of an import or export.
type ExternDesc struct {
	Kind     byte // 0x01 func, 0x02 value, 0x03 type, 0x04 component, 0x05 instance, 0x00 core
	TypeIdx  uint32
	Valtype  Valtype // Kind 0x02
	Bound    byte    // Kind 0x03: 0x00 eq, 0x01 sub
	BoundIdx uint32  // Kind 0x03 / eq
	CoreSort byte    // Kind 0x00
	CoreIdx  uint32  // Kind 0x00
}

// Alias is a type/func/instance/... alias declaration.
type Alias struct {
	Sort     byte // 0x00 core, 0x01 func, 0x02 value, 0x03 type, 0x04 component, 0x05 instance
	CoreSort byte // when Sort == 0x00
	Target   byte // 0x00 instance-export, 0x01 core-instance-export, 0x02 outer
	InstIdx  uint32
	Name     string // instance-export / core-instance-export
	Count    uint32 // outer
	Index    uint32 // outer
}

// World is a decoded component-type payload: the type-section's type vector
// plus the export section's entries.
type World struct {
	Types   []TypeDef
	Exports []WorldExport
}

// WorldExport is one entry of the component-type payload's export section
// (e.g. exporting the world type under a name).
type WorldExport struct {
	Name    string
	Sort    byte // export sort byte
	Index   uint32
	HasDesc bool
	Desc    ExternDesc
}

// DecodeWorld decodes the `component-type` payload for `world` into a World.
// DecodeWorld decodes the `component-type` payload for an embedded world
// ("fern" or "http") into a World.
func DecodeWorld(world string) (*World, error) {
	payload, err := payloadFor(world)
	if err != nil {
		return nil, err
	}
	return DecodeWorldBytes(payload)
}

// DecodeWorldBytes decodes an arbitrary `component-type` payload (e.g. one
// produced by `wasm-tools component embed` from a user's WIT) into a World —
// the ingestion entry point for bring-your-own WIT (P3).
func DecodeWorldBytes(payload []byte) (*World, error) {
	secs, err := SplitComponentSections(payload)
	if err != nil {
		return nil, err
	}
	w := &World{}
	for _, s := range secs {
		switch s.ID {
		case secType:
			types, err := decodeTypeSection(s.Body)
			if err != nil {
				return nil, fmt.Errorf("type section: %w", err)
			}
			w.Types = types
		case secExport:
			exps, err := decodeExportSection(s.Body)
			if err != nil {
				return nil, fmt.Errorf("export section: %w", err)
			}
			w.Exports = exps
		}
	}
	return w, nil
}

func decodeTypeSection(b []byte) ([]TypeDef, error) {
	count, p, err := readULEB(b)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	var types []TypeDef
	for i := uint64(0); i < count; i++ {
		td, n, err := decodeTypeDef(b[p:])
		if err != nil {
			return nil, fmt.Errorf("type %d: %w", i, err)
		}
		p += n
		types = append(types, td)
	}
	if p != len(b) {
		return nil, fmt.Errorf("type section: %d trailing bytes", len(b)-p)
	}
	return types, nil
}

func encodeTypeSection(types []TypeDef) []byte {
	out := appendULEB(nil, uint64(len(types)))
	for _, t := range types {
		out = encodeTypeDef(out, t)
	}
	return out
}

func decodeTypeDef(b []byte) (TypeDef, int, error) {
	if len(b) == 0 {
		return TypeDef{}, 0, fmt.Errorf("empty typedef")
	}
	tag := b[0]
	switch {
	case tag == tagComponent || tag == tagInstance:
		decls, n, err := decodeDecls(b[1:])
		if err != nil {
			return TypeDef{}, 0, err
		}
		return TypeDef{Tag: tag, Decls: decls}, 1 + n, nil
	case tag == tagFunc:
		f, n, err := decodeFuncType(b)
		if err != nil {
			return TypeDef{}, 0, err
		}
		return TypeDef{Tag: tag, Func: &f}, n, nil
	case tag >= tagBorrow && tag <= tagRecord:
		d, n, err := decodeDefinedType(b)
		if err != nil {
			return TypeDef{}, 0, err
		}
		return TypeDef{Tag: tag, Def: &d}, n, nil
	case isPrimByte(tag):
		return TypeDef{Tag: tag}, 1, nil
	default:
		return TypeDef{}, 0, fmt.Errorf("unknown typedef tag %#x", tag)
	}
}

func encodeTypeDef(out []byte, t TypeDef) []byte {
	switch {
	case t.Tag == tagComponent || t.Tag == tagInstance:
		out = append(out, t.Tag)
		return encodeDecls(out, t.Decls)
	case t.Tag == tagFunc:
		return t.Func.encode(out)
	case t.Tag >= tagBorrow && t.Tag <= tagRecord:
		return t.Def.encode(out)
	default: // primitive
		return append(out, t.Tag)
	}
}

func decodeDecls(b []byte) ([]Decl, int, error) {
	count, p, err := readULEB(b)
	if err != nil {
		return nil, 0, fmt.Errorf("decl count: %w", err)
	}
	var decls []Decl
	for i := uint64(0); i < count; i++ {
		d, n, err := decodeDecl(b[p:])
		if err != nil {
			return nil, 0, fmt.Errorf("decl %d: %w", i, err)
		}
		p += n
		decls = append(decls, d)
	}
	return decls, p, nil
}

func encodeDecls(out []byte, decls []Decl) []byte {
	out = appendULEB(out, uint64(len(decls)))
	for _, d := range decls {
		out = encodeDecl(out, d)
	}
	return out
}

func decodeDecl(b []byte) (Decl, int, error) {
	if len(b) == 0 {
		return Decl{}, 0, fmt.Errorf("empty decl")
	}
	kind := b[0]
	p := 1
	d := Decl{Kind: kind}
	switch kind {
	case 0x01: // type
		td, n, err := decodeTypeDef(b[p:])
		if err != nil {
			return d, 0, err
		}
		p += n
		d.Type = &td
	case 0x02: // alias
		a, n, err := decodeAlias(b[p:])
		if err != nil {
			return d, 0, err
		}
		p += n
		d.Alias = &a
	case 0x03, 0x04: // import / export
		name, n, err := decodeExternName(b[p:])
		if err != nil {
			return d, 0, err
		}
		p += n
		ed, m, err := decodeExternDesc(b[p:])
		if err != nil {
			return d, 0, err
		}
		p += m
		d.Name = name
		d.Extern = &ed
	default:
		return d, 0, fmt.Errorf("unsupported decl kind %#x", kind)
	}
	return d, p, nil
}

func encodeDecl(out []byte, d Decl) []byte {
	out = append(out, d.Kind)
	switch d.Kind {
	case 0x01:
		out = encodeTypeDef(out, *d.Type)
	case 0x02:
		out = encodeAlias(out, *d.Alias)
	case 0x03, 0x04:
		out = encodeExternName(out, d.Name)
		out = encodeExternDesc(out, *d.Extern)
	}
	return out
}

// decodeExternName reads a label-kind name: 0x00 + uleb-len + bytes.
func decodeExternName(b []byte) (string, int, error) {
	if len(b) == 0 || b[0] != 0x00 {
		return "", 0, fmt.Errorf("extern name: bad kind byte")
	}
	name, n, err := readName(b[1:])
	if err != nil {
		return "", 0, err
	}
	return name, 1 + n, nil
}

func encodeExternName(out []byte, name string) []byte {
	out = append(out, 0x00)
	return appendName(out, name)
}

func decodeExternDesc(b []byte) (ExternDesc, int, error) {
	if len(b) == 0 {
		return ExternDesc{}, 0, fmt.Errorf("empty externdesc")
	}
	kind := b[0]
	p := 1
	ed := ExternDesc{Kind: kind}
	switch kind {
	case 0x01, 0x04, 0x05: // func / component / instance — a typeidx
		v, n, err := readULEB(b[p:])
		if err != nil {
			return ed, 0, err
		}
		p += n
		ed.TypeIdx = uint32(v)
	case 0x02: // value
		vt, n, err := decodeValtype(b[p:])
		if err != nil {
			return ed, 0, err
		}
		p += n
		ed.Valtype = vt
	case 0x03: // type bound
		if p >= len(b) {
			return ed, 0, fmt.Errorf("type bound: truncated")
		}
		ed.Bound = b[p]
		p++
		if ed.Bound == 0x00 { // eq <typeidx>
			v, n, err := readULEB(b[p:])
			if err != nil {
				return ed, 0, err
			}
			p += n
			ed.BoundIdx = uint32(v)
		} else if ed.Bound != 0x01 { // 0x01 = sub (resource)
			return ed, 0, fmt.Errorf("type bound: bad kind %#x", ed.Bound)
		}
	case 0x00: // core
		if p >= len(b) {
			return ed, 0, fmt.Errorf("core externdesc: truncated")
		}
		ed.CoreSort = b[p]
		p++
		v, n, err := readULEB(b[p:])
		if err != nil {
			return ed, 0, err
		}
		p += n
		ed.CoreIdx = uint32(v)
	default:
		return ed, 0, fmt.Errorf("unknown externdesc kind %#x", kind)
	}
	return ed, p, nil
}

func encodeExternDesc(out []byte, ed ExternDesc) []byte {
	out = append(out, ed.Kind)
	switch ed.Kind {
	case 0x01, 0x04, 0x05:
		out = appendULEB(out, uint64(ed.TypeIdx))
	case 0x02:
		out = ed.Valtype.encode(out)
	case 0x03:
		out = append(out, ed.Bound)
		if ed.Bound == 0x00 {
			out = appendULEB(out, uint64(ed.BoundIdx))
		}
	case 0x00:
		out = append(out, ed.CoreSort)
		out = appendULEB(out, uint64(ed.CoreIdx))
	}
	return out
}

func decodeAlias(b []byte) (Alias, int, error) {
	if len(b) == 0 {
		return Alias{}, 0, fmt.Errorf("empty alias")
	}
	a := Alias{Sort: b[0]}
	p := 1
	if a.Sort == 0x00 { // core sort carries a core-sort byte
		if p >= len(b) {
			return a, 0, fmt.Errorf("alias core sort: truncated")
		}
		a.CoreSort = b[p]
		p++
	}
	if p >= len(b) {
		return a, 0, fmt.Errorf("alias: missing target")
	}
	a.Target = b[p]
	p++
	switch a.Target {
	case 0x00, 0x01: // (core-)instance-export: <instidx> <name>
		v, n, err := readULEB(b[p:])
		if err != nil {
			return a, 0, err
		}
		p += n
		a.InstIdx = uint32(v)
		name, m, err := readName(b[p:])
		if err != nil {
			return a, 0, err
		}
		p += m
		a.Name = name
	case 0x02: // outer: <count> <index>
		c, n, err := readULEB(b[p:])
		if err != nil {
			return a, 0, err
		}
		p += n
		idx, m, err := readULEB(b[p:])
		if err != nil {
			return a, 0, err
		}
		p += m
		a.Count = uint32(c)
		a.Index = uint32(idx)
	default:
		return a, 0, fmt.Errorf("alias: unknown target %#x", a.Target)
	}
	return a, p, nil
}

func encodeAlias(out []byte, a Alias) []byte {
	out = append(out, a.Sort)
	if a.Sort == 0x00 {
		out = append(out, a.CoreSort)
	}
	out = append(out, a.Target)
	switch a.Target {
	case 0x00, 0x01:
		out = appendULEB(out, uint64(a.InstIdx))
		out = appendName(out, a.Name)
	case 0x02:
		out = appendULEB(out, uint64(a.Count))
		out = appendULEB(out, uint64(a.Index))
	}
	return out
}

func decodeExportSection(b []byte) ([]WorldExport, error) {
	count, p, err := readULEB(b)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	var exps []WorldExport
	for i := uint64(0); i < count; i++ {
		name, n, err := decodeExternName(b[p:])
		if err != nil {
			return nil, fmt.Errorf("export %d name: %w", i, err)
		}
		p += n
		if p >= len(b) {
			return nil, fmt.Errorf("export %d: truncated", i)
		}
		e := WorldExport{Name: name, Sort: b[p]}
		p++
		v, m, err := readULEB(b[p:])
		if err != nil {
			return nil, fmt.Errorf("export %d index: %w", i, err)
		}
		p += m
		e.Index = uint32(v)
		// optional externdesc: 0x00 = none, 0x01 = present (then a desc)
		if p >= len(b) {
			return nil, fmt.Errorf("export %d: missing desc tag", i)
		}
		switch b[p] {
		case 0x00:
			p++
		case 0x01:
			p++
			ed, q, err := decodeExternDesc(b[p:])
			if err != nil {
				return nil, fmt.Errorf("export %d desc: %w", i, err)
			}
			p += q
			e.HasDesc = true
			e.Desc = ed
		default:
			return nil, fmt.Errorf("export %d: bad desc tag %#x", i, b[p])
		}
		exps = append(exps, e)
	}
	if p != len(b) {
		return nil, fmt.Errorf("export section: %d trailing bytes", len(b)-p)
	}
	return exps, nil
}

func encodeExportSection(exps []WorldExport) []byte {
	out := appendULEB(nil, uint64(len(exps)))
	for _, e := range exps {
		out = encodeExternName(out, e.Name)
		out = append(out, e.Sort)
		out = appendULEB(out, uint64(e.Index))
		if e.HasDesc {
			out = append(out, 0x01)
			out = encodeExternDesc(out, e.Desc)
		} else {
			out = append(out, 0x00)
		}
	}
	return out
}
