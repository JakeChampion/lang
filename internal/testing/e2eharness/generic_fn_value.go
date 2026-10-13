package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// GenericFnValueProgram names a generic function as a value in every position
// a function type can be wanted at: an argument to a plain function, to a
// generic one (also in a return) and to a function-typed parameter, a let, a
// struct field and a return, with a bounded and an unbounded generic; and
// inside another generic, where the callable it is wanted at is spelled in
// the encloser's own type variable (#12313): `outer` and `meas_in` through a
// free function, `via_both` through a generic struct's method, and
// `plain_with` through a plain struct's method. main answers 0 when every
// call dispatched to the right instance, and the sum it got otherwise.
const GenericFnValueProgram = `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Rect): i32 { return self.w * self.h; } }

function measure[T: Shape](s: T): i32 { return s.area(); }
function ident[T](x: T): T { return x; }
function apply(f: (Sq) => i32, v: Sq): i32 { return f(v); }
function apply_r(f: (Rect) => i32, v: Rect): i32 { return f(v); }
function apply_i(f: (i32) => i32, v: i32): i32 { return f(v); }
function twice[A](f: (A) => i32, v: A): i32 { return f(v) * 2; }
struct Holder { f: (Rect) => i32 }
function pick(): (Sq) => i32 { return measure; }
function apply_to[T: Shape](f: (T) => i32, v: T): i32 { return f(v); }
function use2(g: ((Sq) => i32, Sq) => i32): i32 { return g(measure, Sq { s: 2 }); }
function twice_sq(): i32 { return twice(measure, Sq { s: 1 }); }
function inner[T](acc: T, f: (i32, T) => T): T { return f(1, acc); }
function skip[T](n: i32, v: T): T { return v; }
function outer[T](acc: T): T { return inner(acc, skip); }
function meas_in[T: Shape](s: T): i32 { return apply_to(measure, s); }
struct Reg[P] { p: P, n: i32 }
function (r: Reg[P]) with_h[P](h: (P) => i32): Reg[P] { return Reg[P] { p: r.p, n: r.n + h(r.p) }; }
function one[P](p: P): i32 { return 1; }
function via_both[P](seed: P): i32 { let r: Reg[P] = Reg[P] { p: seed, n: 2 }; return r.with_h(one).n; }
struct Plain { n: i32 }
function (s: Plain) apply_h(h: (i32) => i32): i32 { return h(s.n) + s.n; }
function plain_with(): i32 { let s: Plain = Plain { n: 4 }; return s.apply_h(one); }

function main(): i32 {
    let g: (Rect) => i32 = measure;
    let h: Holder = Holder { f: measure };
    let p: (Sq) => i32 = pick();
    let id: (i32) => i32 = ident;
    // 9 + 10 + 1 + 4 + 4 + 2 + 4 + 5 + 3 + 2 + 7 + 4 + 3 + 5
    let n: i32 = apply(measure, Sq { s: 3 }) + apply_r(measure, Rect { w: 2, h: 5 }) + g(Rect { w: 1, h: 1 })
        + h.f(Rect { w: 2, h: 2 }) + p(Sq { s: 2 }) + twice(measure, Sq { s: 1 }) + use2(apply_to)
        + id(5) + apply_i(ident, 3) + twice_sq() + outer(7) + meas_in(Sq { s: 2 }) + via_both(true) + plain_with();
    if (n == 63) {
        return 0;
    }
    return n;
}
`

// WriteGenericFnValueProgram writes the program into a fresh directory and
// returns its path.
func WriteGenericFnValueProgram(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(GenericFnValueProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
