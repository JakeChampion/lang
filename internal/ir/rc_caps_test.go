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

// The owned-by-default shape is gated on the deep drop being WIRED, not on
// the value being string/array-free: an array- or string-carrying enum or
// struct is owned like a box-only one, a non-uniform enum too, and Map /
// unknown / generic-erased shapes stay borrowed.
func TestOwnedByDefaultShapeAdmitsWiredDrops(t *testing.T) {
	b := capsTestBuilder()
	i32 := ast.NumberType{}
	b.info.Enums["Bag"] = &ast.EnumDecl{Name: "Bag", Variants: []ast.EnumVariant{
		{Name: "Keep", Payloads: []ast.Type{ast.ArrayType{Elem: i32}}},
		{Name: "Drop"},
	}}
	b.info.Enums["Shape"] = &ast.EnumDecl{Name: "Shape", Variants: []ast.EnumVariant{
		{Name: "Circle", Payloads: []ast.Type{i32}},
		{Name: "Rect", Payloads: []ast.Type{i32, i32}},
	}}
	b.info.Enums["Tok"] = &ast.EnumDecl{Name: "Tok", Variants: []ast.EnumVariant{
		{Name: "Ident", Payloads: []ast.Type{ast.StringType{}}},
		{Name: "Num", Payloads: []ast.Type{i32}},
	}}
	b.info.Enums["Generic"] = &ast.EnumDecl{Name: "Generic", Variants: []ast.EnumVariant{
		{Name: "Some", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
	}}
	cases := []struct {
		name      string
		t         ast.Type
		wantWired bool // typeDeepDropWired
		wantOwned bool // ownedByDefaultShape
	}{
		{"scalar struct", ast.StructType{Name: "Point"}, true, true},
		{"string-bearing struct", ast.StructType{Name: "Named"}, true, true},
		{"Map-bearing struct", ast.StructType{Name: "Holder"}, false, false},
		{"unknown struct", ast.StructType{Name: "Reader"}, false, false},
		{"scalar enum", ast.EnumType{Name: "Scalar"}, true, true},
		{"array-payload enum", ast.EnumType{Name: "Bag"}, true, true},
		{"non-uniform enum", ast.EnumType{Name: "Shape"}, true, true},
		{"string-payload enum", ast.EnumType{Name: "Tok"}, true, true},
		{"Map-bearing enum", ast.EnumType{Name: "WithMap"}, false, false},
		{"generic-erased enum", ast.EnumType{Name: "Generic"}, false, false},
		{"unknown enum", ast.EnumType{Name: "Ghost"}, false, false},
		{"recursive list", ast.EnumType{Name: "List"}, true, true},
		{"tuple with string", ast.TupleType{Elems: []ast.Type{i32, ast.StringType{}}}, true, true},
		// Arrays and strings are wired but never owned-by-default as bare
		// parameters: arrays carry the consumed-threaded flag protocol.
		{"array of i32", ast.ArrayType{Elem: i32}, true, false},
		{"string", ast.StringType{}, true, false},
		{"slice", ast.SliceType{Elem: i32}, false, false},
		{"closure", &ast.FuncType{}, false, false},
	}
	for _, tc := range cases {
		wired := typeDeepDropWired(tc.t, b.info, map[string]bool{})
		owned := b.ownedByDefaultShape(tc.t)
		if wired != tc.wantWired || owned != tc.wantOwned {
			t.Errorf("%s: (deepDropWired=%v, ownedByDefaultShape=%v), want (%v, %v)",
				tc.name, wired, owned, tc.wantWired, tc.wantOwned)
		}
	}
}

// A consuming arm frees the box at ITS variant's size, so a non-uniform
// enum's arms size independently; a payloadless variant answers with the
// uniform size (the pair-form rebox's layout) or nothing at all.
func TestEnumVariantBoxSize(t *testing.T) {
	i32 := ast.NumberType{}
	shape := &ast.EnumDecl{Name: "Shape", Variants: []ast.EnumVariant{
		{Name: "Circle", Payloads: []ast.Type{i32}},
		{Name: "Rect", Payloads: []ast.Type{i32, i32}},
		{Name: "Dot"},
	}}
	if sz, ok := enumVariantBoxSize(shape, "Circle", 8); !ok || sz != 8 {
		t.Errorf("Circle: (%d, %v), want (8, true)", sz, ok)
	}
	if sz, ok := enumVariantBoxSize(shape, "Rect", 8); !ok || sz != 12 {
		t.Errorf("Rect: (%d, %v), want (12, true)", sz, ok)
	}
	if _, ok := enumVariantBoxSize(shape, "Dot", 8); ok {
		t.Error("Dot: a payloadless variant of a non-uniform enum has no box size")
	}
	opt := &ast.EnumDecl{Name: "Opt", Variants: []ast.EnumVariant{
		{Name: "A", Payloads: []ast.Type{i32}},
		{Name: "B"},
	}}
	if sz, ok := enumVariantBoxSize(opt, "B", 8); !ok || sz != 8 {
		t.Errorf("B: (%d, %v), want the uniform (8, true)", sz, ok)
	}
	generic := &ast.EnumDecl{Name: "G", Variants: []ast.EnumVariant{
		{Name: "Some", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
	}}
	if _, ok := enumVariantBoxSize(generic, "Some", 8); ok {
		t.Error("Some: a generic-erased payload has no static size")
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
