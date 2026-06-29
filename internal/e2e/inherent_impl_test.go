package e2e

import (
	"testing"
)

// Native-backend coverage for inherent impl blocks (`impl Type { … }` with no
// `for Trait`, issue #2700), complementing the self-host IR cases
// (self_host_inherent_impl_ir_test.go). The parser desugars an inherent impl
// like a trait impl — receiver-less functions become associated functions
// (`Type.f(args)`), `self`-taking ones become methods — so these exercise the
// same constructor/method lowering on x86-64 and wasm (arm64 runs in CI).
var inherentImplCases = []struct {
	name string
	src  string
	want int
}{
	// Associated constructor: bind, then sum fields. 3 + 4 = 7.
	{"struct-ctor", `struct Pt { x: i32, y: i32 }
impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } }
function main(): i32 { var p: Pt = Pt.make(3, 4); return p.x + p.y; }`, 7},
	// Associated fn (`Self` return) + a `self` method on the same type. 7.
	{"assoc-and-method", `struct Pt { x: i32, y: i32 }
impl Pt {
	function make(a: i32, b: i32): Self { return Pt { x: a, y: b }; }
	function sum(self: Self): i32 { return self.x + self.y; }
}
function main(): i32 { var p: Pt = Pt.make(3, 4); return p.sum(); }`, 7},
	// Generic inherent impl: Box.of(42).get(). 42.
	{"generic", `struct Box[T] { v: T }
impl[T] Box[T] {
	function of(v: T): Box[T] { return Box { v: v }; }
	function get(self: Self): T { return self.v; }
}
function main(): i32 { var b: Box[i32] = Box.of(42); return b.get(); }`, 42},
	// Inherent associated fn on an enum returning the enum (nominal). 7.
	{"enum-ctor", `enum E { A(i32), B }
impl E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } }
function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; }
function main(): i32 { return val(E.tag(7)); }`, 7},
}

func TestX86_64InherentImpl(t *testing.T) {
	for _, c := range inherentImplCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestArm64InherentImpl(t *testing.T) {
	for _, c := range inherentImplCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestWASMInherentImpl(t *testing.T) {
	for _, c := range inherentImplCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
