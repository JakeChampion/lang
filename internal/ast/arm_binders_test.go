package ast

import (
	"reflect"
	"sort"
	"testing"
)

// armBinderFields lists every field of MatchArm / MatchExprArm /
// TuplePatElem that names something, and says whether the arm walks yield
// it. A new field fails this test rather than going missing: a binder these
// walks skip is a name the import rewriter mangles into a module decl, the
// renamer collapses onto another declaration's slot, and constfold
// substitutes a const over — one omission, three silent wrong answers
// (#8607).
var armBinderFields = map[string]bool{
	// MatchArm / MatchExprArm.
	"Bindings":  true,
	"AtBinding": true,
	"Payloads":  true,

	"TupleElems":    true,
	"VariantName":   false, // the pattern's TAG, not a binder
	"VariantModule": false,
	"FieldNames":    false, // the projected FIELD, parallel to Bindings
	"EnumName":      false,

	// TuplePatElem.
	"Name":              true,
	"VariantBindings":   true,
	"VariantPayloads":   true,
	"Nested":            true,
	"VariantFieldNames": false,
}

// TestArmBindersCoverEveryNameField pins the field list the walks have to
// keep up with. Only string-ish and pattern-shaped fields are considered —
// the type / position / flag fields cannot carry a name.
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

// armWithEveryBinderPosition is one arm carrying a binder in every position
// the walks claim to reach, with a distinct type per position so BinderType's
// answers are told apart.
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

// BinderType must answer for every name Binders lists — the two walks read
// the same fields, and a position one reaches and the other does not is the
// drift this pairing exists to prevent.
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

// A position whose type list is shorter than its binder list answers nil
// rather than reading past the end: the checker fills these in step, and a
// panic here would turn a stamping gap into a compiler crash.
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
