package checker

import (
	"strings"
	"testing"
)

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
	wantE050Move(t, "concrete own-self double use", `enum List { Cons(i32, List), Nil }
function (own xs: List) head_or(): i32 { match (xs) { Cons(h, t) => { return h; }, Nil => { return 0; } } }
function f(own xs: List): i32 { let a: i32 = xs.head_or(); return a + xs.head_or(); }`)

	wantE050Move(t, "dyn own-self double use", `struct Counter { n: i32 }
trait Consume { function take(own self: Self): i32; }
impl Consume for Counter { function take(own self: Self): i32 { return self.n; } }
function use_dyn(own d: dyn Consume): i32 { let a: i32 = d.take(); return a + d.take(); }`)

	wantOK(t, "borrowed method multi-use", `enum List { Cons(i32, List), Nil }
function (xs: List) len(): i32 { match (xs) { Cons(h, t) => { return 1 + t.len(); }, Nil => { return 0; } } }
function f(own xs: List): i32 { let a: i32 = xs.len(); return a + xs.len(); }`)

	wantOK(t, "single consume", `struct Counter { n: i32 }
trait Consume { function take(own self: Self): i32; }
impl Consume for Counter { function take(own self: Self): i32 { return self.n; } }
function use_dyn(d: dyn Consume): i32 { return d.take(); }`)
}
