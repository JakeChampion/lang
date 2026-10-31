package checker

import (
	"strings"
	"testing"
)

const receiverBoundTraits = `trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { return self - other; } }
struct Box[T] { v: T }
struct Pair[A, B] { a: A, b: B }
struct NoOrd { n: i32 }
`

func TestReceiverBoundAppliesInTheBody(t *testing.T) {
	for _, src := range []string{
		`function (b: Box[T: Ord]) bigger(o: T): T { if (b.v.cmp(o) > 0) { return b.v; } return o; }
function main(): i32 { let b: Box[i32] = Box { v: 3 }; return b.bigger(7); }`,
		`function (p: Pair[A: Ord, B]) left_max(o: A): A { if (p.a.cmp(o) > 0) { return p.a; } return o; }
function main(): i32 { let p: Pair[i32, string] = Pair { a: 3, b: "s" }; return p.left_max(1); }`,
		`function (xs: (T: Ord)[]) peak(): T { let best: T = xs[0]; for x in xs { if (x.cmp(best) > 0) { best = x; } } return best; }
function main(): i32 { let xs: i32[] = [4, 9, 2]; return xs.peak(); }`,
		`function (b: Box[T: Ord]) pick[U](u: U): U { return u; }
function main(): i32 { let b: Box[i32] = Box { v: 3 }; return b.pick(5); }`,
	} {
		if err := checkSource(t, receiverBoundTraits+src); err != nil {
			t.Errorf("a receiver bound did not type-check:\n%s\n%v", src, err)
		}
	}
}

func TestReceiverBoundIsCheckedAtTheCall(t *testing.T) {
	for _, src := range []string{
		`function (b: Box[T: Ord]) get(): T { return b.v; }
function main(): i32 { let b: Box[NoOrd] = Box { v: NoOrd { n: 1 } }; return b.get().n; }`,
		`function (xs: (T: Ord)[]) first(): T { return xs[0]; }
function main(): i32 { let xs: NoOrd[] = [NoOrd { n: 1 }]; return xs.first().n; }`,
	} {
		err := checkSource(t, receiverBoundTraits+src)
		if err == nil {
			t.Errorf("a receiver bound was not checked at the call:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "does not implement trait Ord") {
			t.Errorf("want the unmet-bound error naming trait Ord, got %v", err)
		}
	}
}

func TestReceiverWithoutBoundHasNoMethods(t *testing.T) {
	err := checkSource(t, receiverBoundTraits+`function (b: Box[T]) bad(o: T): i32 { return b.v.cmp(o); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), `no method "cmp" on type parameter T`) {
		t.Errorf("want the missing-bound error, got %v", err)
	}
}
