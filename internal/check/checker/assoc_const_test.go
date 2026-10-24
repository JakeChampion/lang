package checker

import (
	"strings"
	"testing"
)

func TestAssociatedConstsAccepted(t *testing.T) {
	src := `trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64 }
enum Shape { Dot, Line(i32) }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self.LANES; }
}
impl Lanes for f64 {
    const LANES: i32 = 1;
    function width(self: Self): i32 { return Self.LANES; }
}
impl Lanes for Shape {
    const LANES: i32 = 3;
    function width(self: Self): i32 { return Shape.LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T.LANES; }
function main(): i32 {
    let LANES: i32 = 9;
    let d: Shape = Shape.Dot;
    let s: string = V2.NAME;
    return V2.LANES + f64.LANES + V2.LANES + chunk[V2](8) + d.width() + s.len() + LANES;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssociatedConstsErrors(t *testing.T) {
	const decl = `trait Lanes { const LANES: i32; function lanes(): i32; }
struct V2 { a: f64 }
`
	const impl = decl + `impl Lanes for V2 { const LANES: i32 = 2; function lanes(): i32 { return 2; } }
`
	cases := []struct {
		name, src, want string
	}{
		{"missing const", decl + `impl Lanes for V2 { function lanes(): i32 { return 2; } }
function main(): i32 { return 0; }`, `missing associated constant "LANES"`},
		{"function for const", decl + `impl Lanes for V2 { function LANES(): i32 { return 2; } function lanes(): i32 { return 2; } }
function main(): i32 { return 0; }`, "V2.LANES is an associated constant of trait Lanes; bind it with `const LANES: i32 = …;`, not a function"},
		{"const for function", decl + `impl Lanes for V2 { const LANES: i32 = 2; const lanes: i32 = 2; }
function main(): i32 { return 0; }`, "V2.lanes is an associated function of trait Lanes; provide it with `function`, not a const"},
		{"wrong type", decl + `impl Lanes for V2 { const LANES: string = "two"; function lanes(): i32 { return 2; } }
function main(): i32 { return 0; }`, "V2.LANES has the wrong signature for trait Lanes: expected () => i32, got () => string"},
		{"read of a function, concrete", impl + `function main(): i32 { return V2.lanes; }`, "V2.lanes is an associated function, not a constant; call it as V2.lanes(...)"},
		{"read of a function, through a bound", impl + `function f[T: Lanes](): i32 { return T.lanes; }
function main(): i32 { return f[V2](); }`, "T.lanes is an associated function, not a constant; call it as T.lanes(...)"},
		{"call of a constant, concrete", impl + `function main(): i32 { return V2.LANES(); }`, "V2.LANES is an associated constant; read it as V2.LANES without an argument list"},
		{"call of a constant, through a bound", impl + `function f[T: Lanes](): i32 { return T.LANES(); }
function main(): i32 { return f[V2](); }`, "T.LANES is an associated constant; read it as T.LANES without an argument list"},
		{"unknown, concrete", impl + `function main(): i32 { return V2.WIDTH; }`, `V2 has no associated constant "WIDTH"`},
		{"unknown, through a bound", impl + `function f[T: Lanes](): i32 { return T.WIDTH; }
function main(): i32 { return f[V2](); }`, `T has no associated constant "WIDTH"`},
		{"misspelt variant stays a variant error", `enum Kind { A, B }
function main(): i32 { let k: Kind = Kind.C; return 0; }`, `enum Kind has no variant "C"`},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not contain %q", c.name, err.Error(), c.want)
		}
	}
}

func TestAssocTypeBindingProjectsOwnParameter(t *testing.T) {
	src := `trait Lanes {
    type Elem;
    function first(self: Self): Self::Elem;
}
impl Lanes for f64 {
    type Elem = f64;
    function first(self: Self): Self::Elem { return self; }
}
struct Dual[T] { v: T, d: T }
impl[T: Lanes] Lanes for Dual[T] {
    type Elem = T::Elem;
    function first(self: Self): Self::Elem { return self.v.first(); }
}
function main(): i32 {
    let x: Dual[f64] = Dual { v: 1.5, d: 1.0 };
    let e: f64 = x.first();
    return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
