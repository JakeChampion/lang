package interp

// A builtin can write through an array box. A send copy needs a new box for
// every array, including arrays inside records, tuples and enum payloads.
func sendCopyValue(v Value) Value {
	return sendCopier{}.copy(v)
}

// sendCopier holds the copy of each box, so a box that two parts of a value
// hold has one copy.
type sendCopier map[any]Value

func (c sendCopier) copy(v Value) Value {
	switch x := v.(type) {
	case Array:
		whole := x.n == wholeBox
		if whole {
			if done, ok := c[x.b]; ok {
				return done
			}
		}
		out := newArray(len(x.Elems()))
		if whole {
			c[x.b] = out
		}
		for i, item := range x.Elems() {
			out.Elems()[i] = c.copy(item)
			storeArray(out.Elems()[i])
		}
		return out
	case *Struct:
		if done, ok := c[x]; ok {
			return done
		}
		out := &Struct{TypeName: x.TypeName, Fields: make(map[string]Value, len(x.Fields))}
		c[x] = out
		for name, field := range x.Fields {
			out.Fields[name] = c.copy(field)
			storeArray(out.Fields[name])
		}
		return out
	case *Enum:
		if done, ok := c[x]; ok {
			return done
		}
		out := &Enum{EnumName: x.EnumName, VariantName: x.VariantName, Index: x.Index, Payloads: make([]Value, len(x.Payloads))}
		c[x] = out
		for i, payload := range x.Payloads {
			out.Payloads[i] = c.copy(payload)
			storeArray(out.Payloads[i])
		}
		return out
	case *Map:
		if done, ok := c[x]; ok {
			return done
		}
		out := &Map{keys: make([]Value, len(x.keys)), vals: make([]Value, len(x.vals))}
		c[x] = out
		for i, k := range x.keys {
			out.keys[i] = c.copy(k)
			storeArray(out.keys[i])
		}
		for i, item := range x.vals {
			out.vals[i] = c.copy(item)
			storeArray(out.vals[i])
		}
		return out
	default:
		return v
	}
}
