package componenttype

import "fmt"

const (
	primBool   = 0x7f
	primS8     = 0x7e
	primU8     = 0x7d
	primS16    = 0x7c
	primU16    = 0x7b
	primS32    = 0x7a
	primU32    = 0x79
	primS64    = 0x78
	primU64    = 0x77
	primF32    = 0x76
	primF64    = 0x75
	primChar   = 0x74
	primString = 0x73
)

const (
	tagBorrow  = 0x68
	tagOwn     = 0x69
	tagResult  = 0x6a
	tagOption  = 0x6b
	tagEnum    = 0x6d
	tagFlags   = 0x6e
	tagTuple   = 0x6f
	tagList    = 0x70
	tagVariant = 0x71
	tagRecord  = 0x72

	tagFunc = 0x40
)

func isPrimByte(b byte) bool { return b >= primString && b <= primBool }

type Valtype struct {
	IsPrim bool
	Prim   byte
	Idx    uint32
}

func decodeValtype(b []byte) (Valtype, int, error) {
	v, n, err := readSLEB(b)
	if err != nil {
		return Valtype{}, 0, fmt.Errorf("valtype: %w", err)
	}
	if v < 0 {
		prim := byte(v & 0x7f)
		if !isPrimByte(prim) {
			return Valtype{}, 0, fmt.Errorf("valtype: bad primitive code %#x", prim)
		}
		return Valtype{IsPrim: true, Prim: prim}, n, nil
	}
	return Valtype{Idx: uint32(v)}, n, nil
}

func (v Valtype) encode(out []byte) []byte {
	if v.IsPrim {
		return append(out, v.Prim)
	}
	return appendSLEB(out, int64(v.Idx))
}

func readSLEB(b []byte) (int64, int, error) {
	var result int64
	var shift uint
	for i, c := range b {
		if i >= 10 {
			return 0, 0, fmt.Errorf("sleb128 too long")
		}
		result |= int64(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 {
			if shift < 64 && c&0x40 != 0 {
				result |= -1 << shift
			}
			return result, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("sleb128 truncated")
}

func appendSLEB(out []byte, v int64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

type NamedValtype struct {
	Name string
	Ty   Valtype
}

type VariantCase struct {
	Name       string
	HasTy      bool
	Ty         Valtype
	HasRefines bool
	Refines    uint32
}

type DefinedType struct {
	Tag      byte
	Fields   []NamedValtype
	Cases    []VariantCase
	Names    []string
	Elem     Valtype
	Elems    []Valtype
	HasOk    bool
	Ok       Valtype
	HasErr   bool
	Err      Valtype
	Resource uint32
}

func decodeDefinedType(b []byte) (DefinedType, int, error) {
	if len(b) == 0 {
		return DefinedType{}, 0, fmt.Errorf("defined type: empty")
	}
	tag := b[0]
	pos := 1
	d := DefinedType{Tag: tag}
	switch tag {
	case tagRecord:
		count, n, err := readULEB(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("record count: %w", err)
		}
		pos += n
		for i := uint64(0); i < count; i++ {
			name, k, err := readName(b[pos:])
			if err != nil {
				return d, 0, fmt.Errorf("record field %d name: %w", i, err)
			}
			pos += k
			vt, m, err := decodeValtype(b[pos:])
			if err != nil {
				return d, 0, fmt.Errorf("record field %d type: %w", i, err)
			}
			pos += m
			d.Fields = append(d.Fields, NamedValtype{Name: name, Ty: vt})
		}
	case tagVariant:
		count, n, err := readULEB(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("variant count: %w", err)
		}
		pos += n
		for i := uint64(0); i < count; i++ {
			name, k, err := readName(b[pos:])
			if err != nil {
				return d, 0, fmt.Errorf("variant case %d name: %w", i, err)
			}
			pos += k
			if pos >= len(b) {
				return d, 0, fmt.Errorf("variant case %d: truncated", i)
			}
			c := VariantCase{Name: name}
			switch b[pos] {
			case 0x00:
				pos++
			case 0x01:
				pos++
				vt, m, err := decodeValtype(b[pos:])
				if err != nil {
					return d, 0, fmt.Errorf("variant case %d type: %w", i, err)
				}
				pos += m
				c.HasTy = true
				c.Ty = vt
			default:
				return d, 0, fmt.Errorf("variant case %d: bad payload tag %#x", i, b[pos])
			}
			if pos >= len(b) {
				return d, 0, fmt.Errorf("variant case %d: missing refines", i)
			}
			switch b[pos] {
			case 0x00:
				pos++
			case 0x01:
				pos++
				r, m, err := readULEB(b[pos:])
				if err != nil {
					return d, 0, fmt.Errorf("variant case %d refines: %w", i, err)
				}
				pos += m
				c.HasRefines = true
				c.Refines = uint32(r)
			default:
				return d, 0, fmt.Errorf("variant case %d: bad refines tag %#x", i, b[pos])
			}
			d.Cases = append(d.Cases, c)
		}
	case tagEnum, tagFlags:
		count, n, err := readULEB(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("enum/flags count: %w", err)
		}
		pos += n
		for i := uint64(0); i < count; i++ {
			name, k, err := readName(b[pos:])
			if err != nil {
				return d, 0, fmt.Errorf("enum/flags name %d: %w", i, err)
			}
			pos += k
			d.Names = append(d.Names, name)
		}
	case tagList, tagOption:
		vt, m, err := decodeValtype(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("list/option elem: %w", err)
		}
		pos += m
		d.Elem = vt
	case tagTuple:
		count, n, err := readULEB(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("tuple count: %w", err)
		}
		pos += n
		for i := uint64(0); i < count; i++ {
			vt, m, err := decodeValtype(b[pos:])
			if err != nil {
				return d, 0, fmt.Errorf("tuple elem %d: %w", i, err)
			}
			pos += m
			d.Elems = append(d.Elems, vt)
		}
	case tagResult:
		ok, hasOk, n, err := decodeOptionalValtype(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("result ok: %w", err)
		}
		pos += n
		er, hasErr, m, err := decodeOptionalValtype(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("result err: %w", err)
		}
		pos += m
		d.HasOk, d.Ok, d.HasErr, d.Err = hasOk, ok, hasErr, er
	case tagOwn, tagBorrow:
		r, n, err := readULEB(b[pos:])
		if err != nil {
			return d, 0, fmt.Errorf("own/borrow resource: %w", err)
		}
		pos += n
		d.Resource = uint32(r)
	default:
		return d, 0, fmt.Errorf("defined type: unknown tag %#x", tag)
	}
	return d, pos, nil
}

func (d DefinedType) encode(out []byte) []byte {
	out = append(out, d.Tag)
	switch d.Tag {
	case tagRecord:
		out = appendULEB(out, uint64(len(d.Fields)))
		for _, f := range d.Fields {
			out = appendName(out, f.Name)
			out = f.Ty.encode(out)
		}
	case tagVariant:
		out = appendULEB(out, uint64(len(d.Cases)))
		for _, c := range d.Cases {
			out = appendName(out, c.Name)
			if c.HasTy {
				out = append(out, 0x01)
				out = c.Ty.encode(out)
			} else {
				out = append(out, 0x00)
			}
			if c.HasRefines {
				out = append(out, 0x01)
				out = appendULEB(out, uint64(c.Refines))
			} else {
				out = append(out, 0x00)
			}
		}
	case tagEnum, tagFlags:
		out = appendULEB(out, uint64(len(d.Names)))
		for _, n := range d.Names {
			out = appendName(out, n)
		}
	case tagList, tagOption:
		out = d.Elem.encode(out)
	case tagTuple:
		out = appendULEB(out, uint64(len(d.Elems)))
		for _, e := range d.Elems {
			out = e.encode(out)
		}
	case tagResult:
		out = encodeOptionalValtype(out, d.Ok, d.HasOk)
		out = encodeOptionalValtype(out, d.Err, d.HasErr)
	case tagOwn, tagBorrow:
		out = appendULEB(out, uint64(d.Resource))
	}
	return out
}

type FuncType struct {
	Params       []NamedValtype
	NamedResults bool
	Result       Valtype
	Results      []NamedValtype
}

func decodeFuncType(b []byte) (FuncType, int, error) {
	if len(b) == 0 || b[0] != tagFunc {
		return FuncType{}, 0, fmt.Errorf("func type: bad tag")
	}
	pos := 1
	var f FuncType
	count, n, err := readULEB(b[pos:])
	if err != nil {
		return f, 0, fmt.Errorf("func params count: %w", err)
	}
	pos += n
	for i := uint64(0); i < count; i++ {
		name, k, err := readName(b[pos:])
		if err != nil {
			return f, 0, fmt.Errorf("func param %d name: %w", i, err)
		}
		pos += k
		vt, m, err := decodeValtype(b[pos:])
		if err != nil {
			return f, 0, fmt.Errorf("func param %d type: %w", i, err)
		}
		pos += m
		f.Params = append(f.Params, NamedValtype{Name: name, Ty: vt})
	}
	if pos >= len(b) {
		return f, 0, fmt.Errorf("func type: missing results")
	}
	switch b[pos] {
	case 0x00:
		pos++
		vt, m, err := decodeValtype(b[pos:])
		if err != nil {
			return f, 0, fmt.Errorf("func result: %w", err)
		}
		pos += m
		f.Result = vt
	case 0x01:
		pos++
		f.NamedResults = true
		rc, m, err := readULEB(b[pos:])
		if err != nil {
			return f, 0, fmt.Errorf("func named-results count: %w", err)
		}
		pos += m
		for i := uint64(0); i < rc; i++ {
			name, k, err := readName(b[pos:])
			if err != nil {
				return f, 0, fmt.Errorf("func result %d name: %w", i, err)
			}
			pos += k
			vt, q, err := decodeValtype(b[pos:])
			if err != nil {
				return f, 0, fmt.Errorf("func result %d type: %w", i, err)
			}
			pos += q
			f.Results = append(f.Results, NamedValtype{Name: name, Ty: vt})
		}
	default:
		return f, 0, fmt.Errorf("func type: bad results tag %#x", b[pos])
	}
	return f, pos, nil
}

func (f FuncType) encode(out []byte) []byte {
	out = append(out, tagFunc)
	out = appendULEB(out, uint64(len(f.Params)))
	for _, p := range f.Params {
		out = appendName(out, p.Name)
		out = p.Ty.encode(out)
	}
	if f.NamedResults {
		out = append(out, 0x01)
		out = appendULEB(out, uint64(len(f.Results)))
		for _, r := range f.Results {
			out = appendName(out, r.Name)
			out = r.Ty.encode(out)
		}
	} else {
		out = append(out, 0x00)
		out = f.Result.encode(out)
	}
	return out
}

func decodeOptionalValtype(b []byte) (Valtype, bool, int, error) {
	if len(b) == 0 {
		return Valtype{}, false, 0, fmt.Errorf("optional valtype: empty")
	}
	switch b[0] {
	case 0x00:
		return Valtype{}, false, 1, nil
	case 0x01:
		vt, n, err := decodeValtype(b[1:])
		if err != nil {
			return Valtype{}, false, 0, err
		}
		return vt, true, 1 + n, nil
	default:
		return Valtype{}, false, 0, fmt.Errorf("optional valtype: bad tag %#x", b[0])
	}
}

func encodeOptionalValtype(out []byte, v Valtype, has bool) []byte {
	if !has {
		return append(out, 0x00)
	}
	out = append(out, 0x01)
	return v.encode(out)
}

func appendName(out []byte, s string) []byte {
	out = appendULEB(out, uint64(len(s)))
	return append(out, s...)
}
