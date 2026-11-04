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
	default:
		return v
	}
}
