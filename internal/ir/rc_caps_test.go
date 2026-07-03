package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// The capability table (#4477) folded the RC layer's scattered
// type-classification predicates into rc_caps.go. These tests pin the shared
// walker's conservative defaults and cycle behaviour — the edges the
// predecessor walkers (typeIsStringArrayFree,
// typeTransitivelyContainsMap / enumTransitivelyContainsMap) each encoded on
// their own and that a port or later edit could silently soften. Placement
// behaviour stays pinned by the op-count tests + the A/B byte-identity
// differential gate; this file is only about the classification verdicts.

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

func TestRcTypeFactsVerdicts(t *testing.T) {
	b := capsTestBuilder()
	cases := []struct {
		name     string
		t        ast.Type
		wantFree bool
		wantMap  bool
	}{
		{"scalar", ast.NumberType{}, true, false},
		{"string", ast.StringType{}, false, false},
		{"array of i32", ast.ArrayType{Elem: ast.NumberType{}}, false, false},
		// The Map fact recurses through array / slice elements even though
		// the string/array-free fact is already decided.
		{"array of Map-holder", ast.ArrayType{Elem: ast.StructType{Name: "Holder"}}, false, true},
		{"slice of Map-holder", ast.SliceType{Elem: ast.StructType{Name: "Holder"}}, false, true},
		{"Map itself", ast.StructType{Name: "Map"}, false, true},
		{"scalar struct", ast.StructType{Name: "Point"}, true, false},
		{"string-bearing struct", ast.StructType{Name: "Named"}, false, false},
		{"Map-bearing struct", ast.StructType{Name: "Holder"}, false, true},
		// Unknown struct decl (runtime handles: Reader / Writer / MapIter):
		// not free, but no Map either.
		{"unknown struct", ast.StructType{Name: "Reader"}, false, false},
		// Unknown enum decl (generic-erased): the worst verdict on BOTH axes.
		{"unknown enum", ast.EnumType{Name: "Ghost"}, false, true},
		{"scalar enum", ast.EnumType{Name: "Scalar"}, true, false},
		{"Map-bearing enum", ast.EnumType{Name: "WithMap"}, false, true},
		// Recursive enum/struct cycle: the back-edge is assumed clean, so a
		// scalar list/tree stays free on both axes.
		{"recursive list", ast.EnumType{Name: "List"}, true, false},
		{"tuple of scalar+struct", ast.TupleType{Elems: []ast.Type{ast.NumberType{}, ast.StructType{Name: "Point"}}}, true, false},
		{"tuple with string", ast.TupleType{Elems: []ast.Type{ast.NumberType{}, ast.StringType{}}}, false, false},
		// Closures / unresolved generics: not free, no Map.
		{"func type", &ast.FuncType{}, false, false},
		{"param type", ast.ParamType{Name: "T"}, false, false},
	}
	for _, tc := range cases {
		free, hasMap := b.rcTypeFacts(tc.t, map[string]bool{})
		if free != tc.wantFree || hasMap != tc.wantMap {
			t.Errorf("%s: rcTypeFacts = (free=%v, map=%v), want (free=%v, map=%v)",
				tc.name, free, hasMap, tc.wantFree, tc.wantMap)
		}
	}
}

// The two shape axes must stay distinct: the zero-init class adds strings to
// the pointer-shaped set, and the sweep set adds `dyn` only where the backend
// can reclaim it (b.dynReclaim). The exit sweep and the entry safety-zero
// read these axes, so widening one without the other desynchronises them.
func TestRcShapeAxes(t *testing.T) {
	ptr := []ast.Type{
		ast.ArrayType{Elem: ast.NumberType{}},
		ast.StructType{Name: "Point"},
		ast.EnumType{Name: "Scalar"},
		&ast.FuncType{},
		ast.TupleType{Elems: []ast.Type{ast.NumberType{}}},
	}
	for _, ty := range ptr {
		if !rcPtrShaped(ty) || !rcZeroInitClass(ty) {
			t.Errorf("%T: want pointer-shaped and zero-init", ty)
		}
	}
	if rcPtrShaped(ast.StringType{}) {
		t.Error("string must NOT be pointer-shaped (never inc'd as an array element)")
	}
	if !rcZeroInitClass(ast.StringType{}) {
		t.Error("string must be in the zero-init class")
	}
	if rcZeroInitClass(ast.NumberType{}) || rcZeroInitClass(ast.DynTraitType{Traits: []string{"T"}}) {
		t.Error("scalars and dyn must not be zero-init class")
	}
	// dyn joins the sweep set only when the backend has a __drop_dyn helper.
	wasm := &builder{ptrW: 4}
	arm64 := &builder{ptrW: 8}
	x86 := &builder{ptrW: 8, dynRcSupported: true}
	dyn := ast.DynTraitType{Traits: []string{"T"}}
	if !wasm.rcSlotTracked(dyn) || !x86.rcSlotTracked(dyn) {
		t.Error("dyn must be slot-tracked on wasm and x86-64 (reclaimable)")
	}
	if arm64.rcSlotTracked(dyn) {
		t.Error("dyn must NOT be slot-tracked on arm64 (no __drop_dyn helper)")
	}
}
