package ast

import (
	"reflect"
	"sort"
	"testing"
)

// armBinderFields lists every field of MatchArm / MatchExprArm /
// TuplePatElem that names something, and says whether EachArmBinder yields
// it. A new field fails this test rather than going missing from the walk:
// a binder the walk skips is a name the mangler rewrites into a module decl,
// the renamer collapses onto another declaration's slot, and constfold
// substitutes a const over — three silent wrong answers per omission
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

// TestEachArmBinderCoversEveryNameField pins the field list the walk has to
// keep up with. Only string-ish and pattern-shaped fields are considered —
// the type / position / flag fields cannot carry a name.
func TestEachArmBinderCoversEveryNameField(t *testing.T) {
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
					"and to ast.EachArmBinder if it can carry a binder", typ.Name(), f.Name)
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

// TestEachArmBinderReachesEveryPosition drives one arm carrying a binder in
// every position the walk claims to reach, including two levels of tuple
// nesting and a payload sub-pattern inside a tuple element.
func TestEachArmBinderReachesEveryPosition(t *testing.T) {
	arm := &MatchArm{
		Bindings:  []string{"payload", ""},
		AtBinding: "whole",
		Payloads: []*TuplePatElem{
			nil,
			{VariantBindings: []string{"sub"}},
		},
		TupleElems: []TuplePatElem{
			{Name: "elem"},
			{AtBinding: "elem_at", VariantBindings: []string{"elem_variant"}},
			{Nested: []TuplePatElem{
				{Name: "deep"},
				{VariantPayloads: []*TuplePatElem{{Name: "deep_sub"}}},
			}},
		},
	}
	got := ArmBinderNames(arm)
	sort.Strings(got)
	want := []string{"deep", "deep_sub", "elem", "elem_at", "elem_variant", "payload", "sub", "whole"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ArmBinderNames = %v, want %v", got, want)
	}
}

// TestEachArmBinderWritesThrough — the renamer edits the tree through the
// pointer the walk hands it, so every position has to be addressable rather
// than a copy.
func TestEachArmBinderWritesThrough(t *testing.T) {
	arm := &MatchArm{
		Bindings:   []string{"a"},
		AtBinding:  "b",
		Payloads:   []*TuplePatElem{{Name: "c"}},
		TupleElems: []TuplePatElem{{Name: "d", Nested: []TuplePatElem{{Name: "e"}}}},
	}
	EachArmBinder(arm, func(n *string) { *n = *n + "$1" })
	if arm.Bindings[0] != "a$1" || arm.AtBinding != "b$1" || arm.Payloads[0].Name != "c$1" ||
		arm.TupleElems[0].Name != "d$1" || arm.TupleElems[0].Nested[0].Name != "e$1" {
		t.Errorf("a rename did not reach every position: %+v", arm)
	}
}

// TestEachArmBinderSkipsEmptyNames — an empty Bindings entry is the
// placeholder for a slot whose sub-pattern binds instead, and an empty
// element name means the element is a wildcard, a literal or a sub-pattern.
// Neither is a name, so no caller should have to filter them.
func TestEachArmBinderSkipsEmptyNames(t *testing.T) {
	arm := &MatchArm{
		Bindings:   []string{"", "x", ""},
		TupleElems: []TuplePatElem{{IsWildcard: true}, {Name: ""}, {VariantBindings: []string{""}}},
	}
	if got := ArmBinderNames(arm); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("ArmBinderNames = %v, want [x]", got)
	}
}

// TestEachArmExprBinderMatchesStatementForm — the two arm types carry the
// same pattern fields, so the same pattern must produce the same set. They
// drifted before: modload walked the statement form and not this one, so a
// `var v = match (…) { Wrap(helper) => … }` in an imported module mangled its
// binder even in the plain payload position.
func TestEachArmExprBinderMatchesStatementForm(t *testing.T) {
	stmtArm := &MatchArm{
		Bindings:   []string{"p"},
		AtBinding:  "w",
		Payloads:   []*TuplePatElem{{Name: "s"}},
		TupleElems: []TuplePatElem{{Name: "t"}},
	}
	exprArm := &MatchExprArm{
		Bindings:   []string{"p"},
		AtBinding:  "w",
		Payloads:   []*TuplePatElem{{Name: "s"}},
		TupleElems: []TuplePatElem{{Name: "t"}},
	}
	if got, want := ArmExprBinderNames(exprArm), ArmBinderNames(stmtArm); !reflect.DeepEqual(got, want) {
		t.Errorf("expression form yields %v, statement form %v", got, want)
	}
}
