package ir_test

import (
	"testing"
)

// A read out of a FRESH owned container retains once, whether the destination
// is a new binding or an existing slot.
//
// `mk().items` has no local to reclaim the container, so the FieldAccess
// lowering retains the loaded field and deep-drops the container — the value
// arrives already holding its own reference (isOwnedContainerRead). The
// binding path has skipped its alias inc for that reason since #6401; the
// ASSIGNMENT path never did, so `a = mk().items` took two retains against one
// exit dec and pinned the field at rc 1 forever.
//
// Asserted as a differential rather than an absolute count: the two functions
// perform the same read into the same type, so whatever the lowering owes for
// it, they owe the same. That keeps the test honest if the retain count for
// this shape ever changes for an unrelated reason.
func TestOwnedContainerReadRebindsWithOneRetain(t *testing.T) {
	const src = `
struct Box { items: i32[] }

function mkbox(n: i32): Box { return Box { items: [n, n + 1] }; }

function bindIt(n: i32): i32 {
    var a: i32[] = mkbox(n).items;
    return a[0];
}

function assignIt(n: i32): i32 {
    var a: i32[] = [0];
    a = mkbox(n).items;
    return a[0];
}

function main(): i32 { return bindIt(1) + assignIt(2); }
`
	ip := lowerForTest(t, src)
	bind := incCount(funcByName(ip, "bindIt"))
	assign := incCount(funcByName(ip, "assignIt"))
	if assign != bind {
		t.Errorf("assignIt emits %d retains for `a = mkbox(n).items`, bindIt emits %d for the same read bound instead — the extra one is the alias inc the assign path never skipped, and nothing balances it (#8434)", assign, bind)
	}
}
