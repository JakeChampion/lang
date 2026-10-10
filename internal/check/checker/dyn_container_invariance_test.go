package checker

import (
	"strings"
	"testing"
)

const dynShapeDecls = `trait Shape {
    function area(self: Self): i32;
    function sides(self: Self): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function sides(self: Self): i32 { return 4; }
}
function describe(s: dyn Shape): i32 { return s.area() * 10 + s.sides(); }
`

// `assignable` recursed into enum and tuple type ARGUMENTS, and each hop
// re-admitted the `dyn` boxing rule — so `Option[Square]` was assignable to
// `Option[dyn Shape]`. Boxing is a representation change (a `dyn` value is
// [data, vtable]) and it is materialised only at a direct coercion site, so
// the payload slot kept a raw Square pointer: SIGSEGV on the natives, `trap:
// indirect call type mismatch` on wasm, and correct only on interp (#8446).
//
// Structs were invariant here all along — their branch requires an
// argument-free source — so this makes enums and tuples agree with them.
func TestDynIsInvariantInContainerArguments(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"option-from-call", `function mk(): Option[Square] { return Some(Square { side: 3 }); }
function main(): i32 { let o: Option[dyn Shape] = mk(); return 0; }`},
		{"option-from-local", `function main(): i32 { let s: Option[Square] = Some(Square { side: 3 });
    let o: Option[dyn Shape] = s; return 0; }`},
		{"tuple", `function mkt(): (Square, i32) { return (Square { side: 3 }, 7); }
function main(): i32 { let t: (dyn Shape, i32) = mkt(); return t.1; }`},
		{"nested-option", `function mk(): Option[Option[Square]] { return Some(Some(Square { side: 3 })); }
function main(): i32 { let o: Option[Option[dyn Shape]] = mk(); return 0; }`},
		{"result-error-slot", `function mk(): Result[i32, Square] { return Ok(1); }
function main(): i32 { let r: Result[i32, dyn Shape] = mk(); return 0; }`},
		{"argument-position", `function take(o: Option[dyn Shape]): i32 { return 0; }
function mk(): Option[Square] { return Some(Square { side: 3 }); }
function main(): i32 { return take(mk()); }`},
		{"return-position", `function mk(): Option[Square] { return Some(Square { side: 3 }); }
function give(): Option[dyn Shape] { return mk(); }
function main(): i32 { give(); return 0; }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, dynShapeDecls+c.src)
			if err == nil {
				t.Fatal("accepted; no backend boxes a dyn payload inside a container, so this compiles to a raw pointer in a vtable slot")
			}
			if !strings.Contains(err.Error(), "trait object") {
				t.Errorf("the diagnostic does not explain the refusal: %v", err)
			}
		})
	}
}

// The refusal is specific to `dyn`. Every other reason these containers
// assign element-wise has to keep working — that recursion is what lets
// `Option[i64] = Some(1)` settle a polymorphic literal and what the cursor
// idiom's `(Option[T], Stream)` bare-`None` return depends on.
func TestContainerElementAssignabilityStillWorks(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"polymorphic-literal-payload", `function main(): i32 { let o: Option[i64] = Some(1); return 0; }`},
		{"nested-polymorphic-payload", `function main(): i32 { let o: Option[Option[i64]] = Some(Some(1)); return 0; }`},
		{"bare-none-in-tuple", `function f(): (Option[i32], i32) { return (None, 7); }
function main(): i32 { let p = f(); return p.1; }`},
		{"same-dyn-both-sides", `function mk(): Option[dyn Shape] { return Some(Square { side: 3 } as dyn Shape); }
function main(): i32 { let o: Option[dyn Shape] = mk(); return 0; }`},
		{"direct-coercion-still-boxes", `function main(): i32 { let s: dyn Shape = Square { side: 3 }; return describe(s); }`},
		{"explicit-rebuild-is-the-way-out", `function mk(): Option[Square] { return Some(Square { side: 3 }); }
function main(): i32 {
    let o: Option[dyn Shape] = match (mk()) {
        Some(sq) => { Some(sq as dyn Shape) },
        None => { None }
    };
    match (o) { Some(s) => { return describe(s); }, None => { return 1; } }
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, dynShapeDecls+c.src); err != nil {
				t.Errorf("rejected: %v", err)
			}
		})
	}
}

// A direct `dyn` destination is a coercion site, so a refusal there is about
// the source, not about boxing inside a container, and the container hint
// would send the reader to rebuild something that holds nothing.
func TestDirectDynRefusalHasNoContainerHint(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"str-view", `impl Shape for str {
    function area(self: Self): i32 { return self.len(); }
    function sides(self: Self): i32 { return 0; }
}
function main(): i32 { let s: str = "ab"; let d: dyn Shape = s; return d.area(); }`},
		{"no-impl", `function main(): i32 { let d: dyn Shape = 5; return d.area(); }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, dynShapeDecls+c.src)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), "variable \"d\": expected dyn Shape") {
				t.Errorf("want the assignment refusal: %v", err)
			}
			if strings.Contains(err.Error(), "inside a container") {
				t.Errorf("a direct coercion refusal carries the container hint: %v", err)
			}
		})
	}
}
