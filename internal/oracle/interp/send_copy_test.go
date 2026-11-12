package interp

import "testing"

func TestSendCopyKeepsBufferWritesSeparate(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(Array) Value
		leaf func(Value) Array
	}{
		{"array", func(a Array) Value { return a }, func(v Value) Array { return v.(Array) }},
		{"record", func(a Array) Value { return &Struct{TypeName: "Packet", Fields: map[string]Value{"data": a}} }, func(v Value) Array { return v.(*Struct).Fields["data"].(Array) }},
		{"tuple", func(a Array) Value { return arrayOf(Number(7), a) }, func(v Value) Array { return v.(Array).Elems()[1].(Array) }},
		{"enum", func(a Array) Value {
			return &Enum{EnumName: "Packet", VariantName: "Data", Index: 2, Payloads: []Value{a}}
		}, func(v Value) Array { return v.(*Enum).Payloads[0].(Array) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := New()
			original := tc.wrap(arrayOf(Number(0), Number(1)))
			copied, err := i.Builtins["__send_copy"].Fn(i, []Value{original})
			if err != nil {
				t.Fatal(err)
			}
			if copied.String() != original.String() {
				t.Fatalf("copy %s differs from original %s", copied.String(), original.String())
			}
			tc.leaf(copied).intoElems()[0] = Number(65)
			if got := tc.leaf(original).Elems()[0]; got != Number(0) {
				t.Fatalf("copy write changed the original to %v", got)
			}
		})
	}
}

// A record that two fields hold has one copy, so the time of the copy is
// linear in the number of records (#15436).
func TestSendCopyCopiesASharedBoxOneTime(t *testing.T) {
	var cur Value = &Struct{TypeName: "N", Fields: map[string]Value{"xs": arrayOf(Number(1))}}
	for range 64 {
		cur = &Struct{TypeName: "N", Fields: map[string]Value{"l": cur, "r": cur}}
	}
	i := New()
	copied, err := i.Builtins["__send_copy"].Fn(i, []Value{cur})
	if err != nil {
		t.Fatal(err)
	}
	top := copied.(*Struct)
	if top == cur {
		t.Fatal("the copy is the original record")
	}
	if top.Fields["l"] != top.Fields["r"] {
		t.Fatal("the copy does not share the record that the original shares")
	}
}
