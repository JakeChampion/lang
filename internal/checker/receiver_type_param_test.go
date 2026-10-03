package checker

import (
	"strings"
	"testing"
)

// A method on a concrete receiver binds none of its own type parameters from
// the receiver. The dispatch used to pair the receiver's element with the
// method's first parameter, so `(xs: u8[]) total[T: Off]` called with `[]`
// bound T to u8 and failed the bound, or compiled with T = u8 when u8 met it,
// where the same call on a struct receiver is E040 (#10991).
func TestConcreteReceiverDoesNotBindMethodTypeParam(t *testing.T) {
	cases := []struct{ name, src, display string }{
		{"array", `trait Off { function off(self: Self): i32; }
impl Off for i32 { function off(self: i32): i32 { return self; } }
function (xs: u8[]) total[T: Off](values: T[]): i32 { return values.len(); }
function main(): i32 { let b: u8[] = [1 as u8]; return b.total([]); }`, "Array.total"},
		{"view", `function (xs: [u8]) take[T](values: T[]): u8 { return xs[0]; }
function main(): i32 { let b: [u8] = "hi".as_bytes(); return b.take([]) as i32; }`, "slice.take"},
		{"struct-instance", `struct Box[U] { v: U }
function (b: Box[i32]) count[T](values: T[]): i32 { return values.len(); }
function main(): i32 { let b: Box[i32] = Box { v: 1 }; return b.count([]); }`, "Box.count"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatal("accepted: the receiver bound the method's own type parameter")
			}
			if want := "could not infer type parameter T for " + c.display; !strings.Contains(err.Error(), want) {
				t.Errorf("want %q: %v", want, err)
			}
		})
	}
}

// What a receiver does declare it still binds, beside the method's own
// parameters, and an argument or a written list binds those.
func TestReceiverStillBindsWhatItDeclares(t *testing.T) {
	src := `function (xs: T[]) first(): T { return xs[0]; }
function (xs: T[]) mapped[U](f: (T) => U): U[] { let out: U[] = []; for x in xs { out = out.append(f(x)); } return out; }
struct Box[U] { v: U }
function (b: Box[U]) pair[T](t: T): (U, T) { return (b.v, t); }
function (xs: u8[]) count[T](values: T[]): i32 { return values.len(); }
function main(): i32 {
    let arr: u8[] = [7 as u8, 8 as u8];
    let ys: i32[] = arr.mapped((x: u8) => x as i32 + 1);
    let b: Box[i32] = Box { v: 5 };
    let p: (i32, string) = b.pair("s");
    return arr.first() as i32 + ys[1] + p.0 + arr.count([1 as u8]) + arr.count[i32]([]);
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("rejected: %v", err)
	}
}
