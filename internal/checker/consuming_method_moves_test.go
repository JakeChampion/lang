package checker

import (
	"strings"
	"testing"
)

// A consuming (`own self`) method MOVES its receiver, so the affine
// use-after-move analysis must treat the receiver as consumed — for both
// concrete methods and `dyn Trait` dispatch. Before this, `x.consume();
// x.consume()` slipped past E050 and double-freed at runtime.

func wantE050Move(t *testing.T, name, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("%s: expected E050 (use after move), got none", name)
	}
	if !strings.Contains(err.Error(), "after it was consumed") {
		t.Errorf("%s: expected a use-after-move error, got: %v", name, err)
	}
}

func TestConsumingMethodReceiverMoves(t *testing.T) {
	// Concrete consuming method: second receiver use is a use-after-move.
	wantE050Move(t, "concrete own-self double use", `enum List { Cons(i32, List), Nil }
function (own xs: List) head_or(): i32 { match (xs) { Cons(h, t) => { return h; }, Nil => { return 0; } } }
function f(own xs: List): i32 { var a: i32 = xs.head_or(); return a + xs.head_or(); }`)

	// `dyn Trait` consuming method: same, through dynamic dispatch.
	wantE050Move(t, "dyn own-self double use", `struct Counter { n: i32 }
trait Consume { function take(own self: Self): i32; }
impl Consume for Counter { function take(own self: Self): i32 { return self.n; } }
function use_dyn(own d: dyn Consume): i32 { var a: i32 = d.take(); return a + d.take(); }`)

	// A BORROWED-self method may be called repeatedly on an owned value — no
	// false positive.
	wantOK(t, "borrowed method multi-use", `enum List { Cons(i32, List), Nil }
function (xs: List) len(): i32 { match (xs) { Cons(h, t) => { return 1 + t.len(); }, Nil => { return 0; } } }
function f(own xs: List): i32 { var a: i32 = xs.len(); return a + xs.len(); }`)

	// A single consume is fine.
	wantOK(t, "single consume", `struct Counter { n: i32 }
trait Consume { function take(own self: Self): i32; }
impl Consume for Counter { function take(own self: Self): i32 { return self.n; } }
function use_dyn(d: dyn Consume): i32 { return d.take(); }`)
}
