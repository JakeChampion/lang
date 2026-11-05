package interp

// A builtin can write through an array box. A send copy needs a new box for
// every array, including arrays inside records, tuples and enum payloads.
func sendCopyValue(v Value) Value {
	switch x := v.(type) {
	case Array:
		out := newArray(len(x.Elems()))
		for i, item := range x.Elems() {
			out.Elems()[i] = sendCopyValue(item)
			storeArray(out.Elems()[i])
		}
		return out
	case *Struct:
		out := &Struct{TypeName: x.TypeName, Fields: make(map[string]Value, len(x.Fields))}
		for name, field := range x.Fields {
			out.Fields[name] = sendCopyValue(field)
			storeArray(out.Fields[name])
		}
		return out
	case *Enum:
		out := &Enum{EnumName: x.EnumName, VariantName: x.VariantName, Index: x.Index, Payloads: make([]Value, len(x.Payloads))}
		for i, payload := range x.Payloads {
			out.Payloads[i] = sendCopyValue(payload)
			storeArray(out.Payloads[i])
		}
		return out
	case *Map:
		out := &Map{keys: make([]Value, len(x.keys)), vals: make([]Value, len(x.vals))}
		for i, k := range x.keys {
			out.keys[i] = sendCopyValue(k)
			storeArray(out.keys[i])
		}
		for i, item := range x.vals {
			out.vals[i] = sendCopyValue(item)
			storeArray(out.vals[i])
		}
		return out
	default:
		return v
	}
}
