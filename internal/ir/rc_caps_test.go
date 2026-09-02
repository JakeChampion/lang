package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Pins for the rc_caps.go classification layer (#4477): the transitive
// walkers' conservative defaults and cycle behaviour, and the tracked-set
// layering — the edges a port or a later edit could silently soften.
// Placement behaviour stays pinned by the op-count tests and the e2e
// differential gates; this file is only about the classification verdicts.

func capsTestBuilder() *builder {
	i32 := ast.NumberType{}
	return &builder{info: &checker.Info{
		Structs: map[string]*ast.StructDecl{
			"Point": {Name: "Point", Fields: []ast.Param{
				{Name: "x", Type: i32}, {Name: "y", Type: i32}}},
			"Named": {Name: "Named", Fields: []ast.Param{
				{Name: "n", Type: ast.StringType{}}}},
			"Holder": {Name: "Holder", Fields: []ast.Param{
				{Name: "m", Type: ast.StructType{Name: "Map"}}}},
			// Mutually recursive with the List enum below.
			"Node": {Name: "Node", Fields: []ast.Param{
				{Name: "next", Type: ast.EnumType{Name: "List"}}}},
		},
		Enums: map[string]*ast.EnumDecl{
			"List": {Name: "List", Variants: []ast.EnumVariant{
				{Name: "Cons", Payloads: []ast.Type{ast.StructType{Name: "Node"}}},
				{Name: "Nil"},
			}},
			"WithMap": {Name: "WithMap", Variants: []ast.EnumVariant{
				{Name: "M", Payloads: []ast.Type{ast.StructType{Name: "Holder"}}},
			}},
			"Scalar": {Name: "Scalar", Variants: []ast.EnumVariant{
				{Name: "A", Payloads: []ast.Type{i32}},
				{Name: "B"},
			}},
		},
	}}
}

func TestRcTransitiveClassifierVerdicts(t *testing.T) {
	b := capsTestBuilder()
	cases := []struct {
		name     string
		t        ast.Type
		wantFree bool // typeIsStringArrayFree
		wantMap  bool // typeTransitivelyContainsMap
	}{
		{"scalar", ast.NumberType{}, true, false},
		{"string", ast.StringType{}, false, false},
		{"array of i32", ast.ArrayType{Elem: ast.NumberType{}}, false, false},
		// The Map fact recurses through array / slice elements even though
		// the string/array-free fact is already decided at the array.
		{"array of Map-holder", ast.ArrayType{Elem: ast.StructType{Name: "Holder"}}, false, true},
		{"slice of Map-holder", ast.SliceType{Elem: ast.StructType{Name: "Holder"}}, false, true},
		{"Map itself", ast.StructType{Name: "Map"}, false, true},
		{"scalar struct", ast.StructType{Name: "Point"}, true, false},
		{"string-bearing struct", ast.StructType{Name: "Named"}, false, false},
		{"Map-bearing struct", ast.StructType{Name: "Holder"}, false, true},
		// Unknown struct decl (runtime handles: Reader / Writer / MapIter):
		// not string/array-free, but no Map either.
		{"unknown struct", ast.StructType{Name: "Reader"}, false, false},
		// Unknown enum decl (generic-erased): the worst verdict on BOTH
		// axes — not free AND Map-containing.
		{"unknown enum", ast.EnumType{Name: "Ghost"}, false, true},
		{"scalar enum", ast.EnumType{Name: "Scalar"}, true, false},
		{"Map-bearing enum", ast.EnumType{Name: "WithMap"}, false, true},
		// Recursive enum/struct cycle: back-edges are assumed clean on both
		// axes, so a scalar list/tree stays free and Map-less.
		{"recursive list", ast.EnumType{Name: "List"}, true, false},
		{"tuple of scalar+struct", ast.TupleType{Elems: []ast.Type{ast.NumberType{}, ast.StructType{Name: "Point"}}}, true, false},
		{"tuple with string", ast.TupleType{Elems: []ast.Type{ast.NumberType{}, ast.StringType{}}}, false, false},
		// Closures / unresolved generics: not free, no Map.
		{"func type", &ast.FuncType{}, false, false},
		{"param type", ast.ParamType{Name: "T"}, false, false},
	}
	for _, tc := range cases {
		free := b.typeIsStringArrayFree(tc.t, map[string]bool{})
		hasMap := typeTransitivelyContainsMap(b.info, tc.t, map[string]bool{})
		if free != tc.wantFree || hasMap != tc.wantMap {
			t.Errorf("%s: (stringArrayFree=%v, containsMap=%v), want (%v, %v)",
				tc.name, free, hasMap, tc.wantFree, tc.wantMap)
		}
	}
}

// The tracked-set layering must stay distinct: strings join at the slot
// layer (never the element layer), and `dyn Trait` joins only at the
// sweep layer, only where the backend can reclaim it (b.dynReclaim) —
// the exit sweep and the entry safety-zero both special-case it above
// rcTrackedSlotType (#4495). Widening one layer without the others is
// exactly the bug class this pins.
func TestRcTrackedSetLayering(t *testing.T) {
	ptrShaped := []ast.Type{
		ast.ArrayType{Elem: ast.NumberType{}},
		ast.StructType{Name: "Point"},
		ast.EnumType{Name: "Scalar"},
		&ast.FuncType{},
		ast.TupleType{Elems: []ast.Type{ast.NumberType{}}},
	}
	for _, ty := range ptrShaped {
		if !arrElemIsRcTracked(ty) || !rcTrackedSlotType(ty) {
			t.Errorf("%T: want element-tracked and slot-tracked", ty)
		}
	}
	if arrElemIsRcTracked(ast.StringType{}) {
		t.Error("string must NOT be element-tracked (never inc'd on array insertion)")
	}
	if !rcTrackedSlotType(ast.StringType{}) {
		t.Error("string must be slot-tracked")
	}
	if rcTrackedSlotType(ast.NumberType{}) {
		t.Error("scalars must not be slot-tracked")
	}
	dyn := ast.DynTraitType{Traits: []string{"T"}}
	if rcTrackedSlotType(dyn) || arrElemIsRcTracked(dyn) {
		t.Error("dyn must not be in the shared sets — it is the backend-gated sweep extra")
	}
	// The gate the sweep layer keys on: wasm (ptrW==4) and any native
	// that opted in reclaim dyn; arm64 (ptrW==8, no opt-in) leaks it.
	if !(&builder{ptrW: 4}).dynReclaim() || !(&builder{ptrW: 8, dynRcSupported: true}).dynReclaim() {
		t.Error("dyn must be reclaimable on wasm and opted-in natives")
	}
	if (&builder{ptrW: 8}).dynReclaim() {
		t.Error("dyn must NOT be reclaimable on arm64 (no __drop_dyn helper, slice 4c)")
	}
}
