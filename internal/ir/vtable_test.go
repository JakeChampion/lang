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
