package ast

import (
	"reflect"
	"sort"
	"testing"
)

var armBinderFields = map[string]bool{
	"Bindings":  true,
	"AtBinding": true,
	"Payloads":  true,

	"TupleElems":    true,
	"VariantName":   false,
	"VariantModule": false,
	"FieldNames":    false,
	"EnumName":      false,

	"Name":              true,
	"VariantBindings":   true,
	"VariantPayloads":   true,
	"Nested":            true,
	"VariantFieldNames": false,
}

func TestArmBindersCoverEveryNameField(t *testing.T) {
	seen := map[string]bool{}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(MatchArm{}),
		reflect.TypeOf(MatchExprArm{}),
		reflect.TypeOf(TuplePatElem{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			switch f.Type.String() {
			case "string", "[]string", "[]ast.TuplePatElem", "[]*ast.TuplePatElem":
			default:
				continue
			}
			if _, known := armBinderFields[f.Name]; !known {
				t.Errorf("%s.%s is a new name-shaped field: add it to armBinderFields, "+
					"and to armBinders / BinderType if it can carry a binder", typ.Name(), f.Name)
			}
			seen[f.Name] = true
		}
	}
	for name := range armBinderFields {
		if !seen[name] {
			t.Errorf("armBinderFields lists %q, which no arm type declares any more", name)
		}
	}
}

func armWithEveryBinderPosition() *MatchExprArm {
	return &MatchExprArm{
		Bindings:     []string{"payload", ""},
		BindingTypes: []Type{StringType{}, BoolType{}},
		AtBinding:    "whole",
		Payloads: []*TuplePatElem{
			nil,
			{VariantBindings: []string{"sub"}, VariantBindingTypes: []Type{NumberType{Width: 64, Signed: true}}},
		},
		TupleElems: []TuplePatElem{
			{Name: "elem"},
			{AtBinding: "elem_at", VariantBindings: []string{"elem_variant"}, VariantBindingTypes: []Type{BoolType{}}},
			{
				Nested: []TuplePatElem{
					{Name: "deep"},
					{VariantPayloads: []*TuplePatElem{{Name: "deep_sub"}}, VariantBindingTypes: []Type{StringType{}}},
				},
				NestedTypes: []Type{FloatType{}, StringType{}},
			},
		},
	}
}

func TestBindersReachEveryPosition(t *testing.T) {
	got := armWithEveryBinderPosition().Binders()
	sort.Strings(got)
	want := []string{"deep", "deep_sub", "elem", "elem_at", "elem_variant", "payload", "sub", "whole"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Binders() = %v, want %v", got, want)
	}
}

func TestBinderTypeAnswersForEveryBinder(t *testing.T) {
	arm := armWithEveryBinderPosition()
	scrutinee := Type(TupleType{Elems: []Type{StringType{}}})
	for _, name := range arm.Binders() {
		if got := arm.BinderType(name, scrutinee); got == nil {
			t.Errorf("BinderType(%q) = nil, but Binders lists it", name)
		}
	}
	if got := arm.BinderType("whole", scrutinee); !reflect.DeepEqual(got, scrutinee) {
		t.Errorf("BinderType of the @ binder = %v, want the scrutinee type %v", got, scrutinee)
	}
	if got := arm.BinderType("payload", scrutinee); !reflect.DeepEqual(got, Type(StringType{})) {
		t.Errorf("BinderType(payload) = %v, want string", got)
	}
	if got := arm.BinderType("deep", scrutinee); !reflect.DeepEqual(got, Type(FloatType{})) {
		t.Errorf("BinderType(deep) = %v, want f64 — the nested element type list", got)
	}
	if got := arm.BinderType("nothing_binds_this", scrutinee); got != nil {
		t.Errorf("BinderType of an unbound name = %v, want nil", got)
	}
}

func TestBinderTypeToleratesShortTypeLists(t *testing.T) {
	arm := &MatchExprArm{
		Bindings:   []string{"a"},
		TupleElems: []TuplePatElem{{Name: "b"}, {VariantBindings: []string{"c"}}},
	}
	for _, name := range []string{"a", "b", "c"} {
		if got := arm.BinderType(name, nil); got != nil {
			t.Errorf("BinderType(%q) = %v with no type list, want nil", name, got)
		}
	}
}
