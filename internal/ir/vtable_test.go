package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// checkSourceForVtable parses + type-checks src and returns the program +
// info so a test can call collectVtables directly. `dyn` programs are
// rejected by LowerWith (compiled backends), so the vtable-collection
// foundation is exercised here rather than through the full lower.
func checkSourceForVtable(t *testing.T, src string) (*ast.Program, *checker.Info) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return prog, info
}

// TestCollectVtablesBasic: a trait used in a `dyn` type with two impls
// yields one vtable per implementing concrete type, deterministically
// ordered, with one slot per (non-associated) trait method in
// declaration order, each pointing at the concrete type's mangled impl.
func TestCollectVtablesBasic(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
    function name(self: Self): string;
}
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r * 3; }
    function name(self: Self): string { return "circle"; }
}
impl Shape for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
    function name(self: Self): string { return "rect"; }
}
function describe(s: dyn Shape): string { return s.name(); }
function main(): i32 { return 0; }`
	prog, info := checkSourceForVtable(t, src)
	vts := collectVtables(prog, info)
	if len(vts) != 2 {
		t.Fatalf("want 2 vtables (Circle, Rect), got %d: %+v", len(vts), vts)
	}
	// Deterministic, name-sorted: Circle before Rect.
	if vts[0].Concrete != "Circle" || vts[1].Concrete != "Rect" {
		t.Fatalf("vtables not name-sorted: %q, %q", vts[0].Concrete, vts[1].Concrete)
	}
	for _, vt := range vts {
		if vt.Trait != "Shape" {
			t.Errorf("vtable trait = %q, want Shape", vt.Trait)
		}
		if len(vt.Methods) != 2 {
			t.Fatalf("%s vtable: want 2 method slots, got %d: %+v", vt.Concrete, len(vt.Methods), vt.Methods)
		}
		// Slot order follows trait declaration order: area, then name.
		if vt.Methods[0].Method != "area" || vt.Methods[1].Method != "name" {
			t.Errorf("%s slots out of trait order: %q, %q", vt.Concrete, vt.Methods[0].Method, vt.Methods[1].Method)
		}
		// Each slot points at the concrete type's registered impl.
		for _, m := range vt.Methods {
			want := info.Methods[vt.Concrete+"."+m.Method]
			if want == "" {
				want = "__method_" + vt.Concrete + "_" + m.Method
			}
			if m.Func != want {
				t.Errorf("%s.%s slot func = %q, want %q", vt.Concrete, m.Method, m.Func, want)
			}
		}
	}
}

// TestCollectVtablesDropSlot: the per-concrete vtable records the
// concrete type's drop fn for the trailing drop slot (docs/DYN-TRAITS.md
// §4.4). A struct concrete records __drop_struct_<C> (its heap box — and
// any transitively-owned RC value like a String field — is reclaimed
// through it); a primitive concrete records "" (the null sentinel: the
// value lives inline in `data`, no box to free).
func TestCollectVtablesDropSlot(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Named { tag: string }
struct Flat { r: i32 }
impl Shape for Named {
    function area(self: Self): i32 { return 1; }
}
impl Shape for Flat {
    function area(self: Self): i32 { return self.r; }
}
impl Shape for i32 {
    function area(self: Self): i32 { return self; }
}
function describe(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { return 0; }`
	prog, info := checkSourceForVtable(t, src)
	vts := collectVtables(prog, info)
	byName := map[string]VtableDecl{}
	for _, vt := range vts {
		byName[vt.Concrete] = vt
	}
	// A struct concrete that owns a String field reclaims through its
	// generated struct drop.
	if got := byName["Named"].Drop; got != "__drop_struct_Named" {
		t.Errorf("Named drop slot = %q, want __drop_struct_Named", got)
	}
	// A scalar-only struct concrete still owns its heap box, reclaimed
	// through its generated struct drop (which just frees the box).
	if got := byName["Flat"].Drop; got != "__drop_struct_Flat" {
		t.Errorf("Flat drop slot = %q, want __drop_struct_Flat", got)
	}
	// A primitive concrete's `data` is a heap-boxed VALUE CELL
	// (boxPrimitiveDynValue), reclaimed through the generated
	// __drop_dynprim_<prim> cell free (#4351 — the null sentinel here
	// leaked the cell on every prim coercion).
	if got := byName["i32"].Drop; got != "__drop_dynprim_i32" {
		t.Errorf("i32 (primitive) drop slot = %q, want __drop_dynprim_i32", got)
	}
}

// TestCollectVtablesNoneWhenNoDyn: a trait with impls but no `dyn` use
// anywhere emits no vtables (nothing to dispatch dynamically).
func TestCollectVtablesNoneWhenNoDyn(t *testing.T) {
	src := `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
function main(): i32 { var c: Circle = Circle { r: 2 }; return c.area(); }`
	prog, info := checkSourceForVtable(t, src)
	if vts := collectVtables(prog, info); len(vts) != 0 {
		t.Fatalf("want 0 vtables without any dyn use, got %d: %+v", len(vts), vts)
	}
}

// TestCollectVtablesMultiTrait: a `dyn A + B` use produces ONE merged
// vtable per implementing concrete, keyed by the sorted-set key "A+B",
// whose slots are the CONCATENATION of A's then B's methods (each in
// trait-declaration order). This is the merged-vtable layout the global
// slot math indexes (docs/DYN-TRAITS.md §10).
func TestCollectVtablesMultiTrait(t *testing.T) {
	src := `import "std/i32";
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; function tare(self: Self): i32; }
struct Apple { g: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple {
    function weight(self: Self): i32 { return self.g; }
    function tare(self: Self): i32 { return 5; }
}
function describe(d: dyn Show + Weigh): string { return d.show(); }
function main(): i32 { return 0; }`
	prog, info := checkSourceForVtable(t, src)
	vts := collectVtables(prog, info)
	if len(vts) != 1 {
		t.Fatalf("want 1 merged vtable (Apple), got %d: %+v", len(vts), vts)
	}
	vt := vts[0]
	// Sorted set key: "Show" < "Weigh".
	if vt.Trait != "Show+Weigh" {
		t.Errorf("merged vtable key = %q, want %q", vt.Trait, "Show+Weigh")
	}
	if vt.Concrete != "Apple" {
		t.Errorf("merged vtable concrete = %q, want Apple", vt.Concrete)
	}
	// Concatenation in sorted-set order: Show.show, then Weigh.weight,
	// Weigh.tare (each trait's methods in declaration order).
	wantSlots := []struct{ method, fn string }{
		{"show", "__method_Apple_show"},
		{"weight", "__method_Apple_weight"},
		{"tare", "__method_Apple_tare"},
	}
	if len(vt.Methods) != len(wantSlots) {
		t.Fatalf("merged vtable: want %d slots, got %d: %+v", len(wantSlots), len(vt.Methods), vt.Methods)
	}
	for i, w := range wantSlots {
		if vt.Methods[i].Method != w.method {
			t.Errorf("slot %d method = %q, want %q", i, vt.Methods[i].Method, w.method)
		}
		got := vt.Methods[i].Func
		want := info.Methods["Apple."+w.method]
		if want == "" {
			want = w.fn
		}
		if got != want {
			t.Errorf("slot %d func = %q, want %q", i, got, want)
		}
	}
}

// TestDynTraitMethodPrefixGlobalSlot: a method owned by the 2nd (and
// 3rd) trait in a sorted set gets a non-zero prefix = the sum of the
// earlier traits' non-associated method counts. This is the global-slot
// math OpCallDyn uses (slot = prefix + index-within-owning-trait).
func TestDynTraitMethodPrefixGlobalSlot(t *testing.T) {
	src := `import "std/i32";
trait A { function a1(self: Self): i32; function a2(self: Self): i32; }
trait B { function b1(self: Self): i32; }
trait C { function c1(self: Self): i32; function c2(self: Self): i32; }
struct S { x: i32 }
impl A for S { function a1(self: Self): i32 { return 1; } function a2(self: Self): i32 { return 2; } }
impl B for S { function b1(self: Self): i32 { return 3; } }
impl C for S { function c1(self: Self): i32 { return 4; } function c2(self: Self): i32 { return 5; } }
function pick(d: dyn A + B + C): i32 { return d.a1(); }
function main(): i32 { return 0; }`
	_, info := checkSourceForVtable(t, src)
	set := []string{"A", "B", "C"} // sorted set order
	// A is first → prefix 0; B after A (2 methods) → 2; C after A+B → 3.
	cases := []struct {
		owner string
		want  int
	}{
		{"A", 0},
		{"B", 2},
		{"C", 3},
	}
	for _, c := range cases {
		if got := dynTraitMethodPrefix(info, set, c.owner); got != c.want {
			t.Errorf("prefix(%v, owner=%s) = %d, want %d", set, c.owner, got, c.want)
		}
	}
	// Single-trait set → prefix is always 0 (slot math unchanged).
	if got := dynTraitMethodPrefix(info, []string{"A"}, "A"); got != 0 {
		t.Errorf("single-trait prefix = %d, want 0", got)
	}
}

// buildDynboxWrappers must emit an unboxing wrapper only for a primitive
// concrete some site actually COERCES, not for every implementor
// collectVtables listed.
//
// The over-approximation is deliberate for vtables — an unreferenced one is
// dead static data no backend emits — but a wrapper is dead CODE whose body
// calls `__method_<C>_<m>`. Tree-shake roots only the coerced concretes'
// methods (treeshake.DynCoercionImplMethods), so a wrapper for an
// implementor nothing coerces references a symbol that was dropped and the
// native link fails on an undefined label. Only a coercion site can
// materialise a primitive vtable (`as?` targets are struct/enum), so the
// recorded coercions are the complete set of reachable wrappers.
func TestBuildDynboxWrappersSkipsUncoercedImplementors(t *testing.T) {
	src := `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for i32 { function show(self: Self): i32 { return self; } }
impl Show for boolean { function show(self: Self): i32 { return 1; } }
function main(): i32 {
    var d: dyn Show = 7;
    return d.show();
}`
	prog, info := checkSourceForVtable(t, src)
	vts := collectVtables(prog, info)
	// Both implementors get a vtable — that part is the over-approximation.
	if len(vts) != 2 {
		t.Fatalf("want vtables for both implementors, got %d: %+v", len(vts), vts)
	}
	wrappers, err := buildDynboxWrappers(info, 8, vts)
	if err != nil {
		t.Fatalf("buildDynboxWrappers: %v", err)
	}
	var names []string
	for _, fn := range wrappers {
		names = append(names, fn.Name)
	}
	if len(names) != 1 || names[0] != "__dynbox_i32_show" {
		t.Errorf("wrappers = %v, want only [__dynbox_i32_show] (boolean is never coerced)", names)
	}
}

// astTypeForConcreteName must cover every spelling ast.ReceiverTypeName can
// produce for a non-struct/enum receiver, since that is what a `dyn` set's
// primitive concretes are named by. `u8` was the gap: `core/cmp` declares
// `impl Display for u8`, so the whole `dyn cmp.Display` set failed the
// layout lookup and no compiled backend could lower it.
func TestAstTypeForConcreteNameCoversReceiverSpellings(t *testing.T) {
	spellings := []ast.Type{
		ast.NumberType{Width: 32, Signed: true},
		ast.NumberType{Width: 64, Signed: true},
		ast.NumberType{Width: 32, Signed: false},
		ast.NumberType{Width: 64, Signed: false},
		ast.NumberType{Width: 8, Signed: false},
		ast.FloatType{Width: 32},
		ast.FloatType{Width: 64},
		ast.BoolType{},
		ast.StringType{},
		ast.CharType{},
	}
	for _, ty := range spellings {
		name, ok := ast.ReceiverTypeName(ty)
		if !ok {
			t.Fatalf("%T: ReceiverTypeName reported no name", ty)
		}
		if astTypeForConcreteName(name) == nil {
			t.Errorf("astTypeForConcreteName(%q): no value-box layout", name)
		}
	}
}
