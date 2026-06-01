package ir_test

import "testing"

// Move-on-construction: `var s = Wrap{ inner: x }` where x is an owned
// rc local at its last use moves x into the field — the field-init inc
// and x's exit-sweep dec cancel, so no __fern_rc_inc remains.
func TestMoveOnConstructionElidesIncForLastUse(t *testing.T) {
	ip := lowerForTest(t, `struct Wrap { inner: i32[] }
function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0];
}
function main(): i32 { return f(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("owned local moved into a struct field at last use should emit no __fern_rc_inc, got %d", got)
	}
}

// A local READ again after the construction is NOT at its last use, so
// its field-init inc is kept (the later read needs it live).
func TestMoveOnConstructionKeepsIncWhenReadAgain(t *testing.T) {
	ip := lowerForTest(t, `struct Wrap { inner: i32[] }
function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0] + x[1];
}
function main(): i32 { return f(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got == 0 {
		t.Errorf("construction consuming a multi-use local must keep its inc, got 0")
	}
}

// Tuple literals get the same treatment: an owned rc local consumed as
// a tuple element at its last use is moved into the tuple (__drop_tuple_
// dec's it), so the element inc is elided.
func TestMoveOnConstructionElidesIncForTupleElement(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var t: (i32[], i32) = (x, 9);
    return t.0[0] + t.1;
}
function main(): i32 { return f(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("owned local moved into a tuple element at last use should emit no __fern_rc_inc, got %d", got)
	}
}

// Closure captures get the same treatment: an owned rc local captured
// at its last use is moved into the closure env (the closure's drop
// thunk dec's the capture), so the capture inc is elided.
func TestMoveOnConstructionElidesIncForClosureCapture(t *testing.T) {
	ip := lowerForTest(t, `function f(): () => i32 {
    var x: i32[] = [1, 2, 3];
    function get(): i32 { return x[0]; }
    return get;
}
function main(): i32 { return f()(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("owned local moved into a closure capture at last use should emit no __fern_rc_inc, got %d", got)
	}
}

// A construction nested in a branch is not a top-level statement, so it
// does not dominate the exit — the inc is kept (mirrors the move-on-
// alias branch guard).
func TestMoveOnConstructionKeepsIncForBranched(t *testing.T) {
	ip := lowerForTest(t, `struct Wrap { inner: i32[] }
function f(c: boolean): i32 {
    var x: i32[] = [1, 2, 3];
    if (c) {
        var s: Wrap = Wrap { inner: x };
        return s.inner[0];
    }
    return x[0];
}
function main(): i32 { return f(true); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got == 0 {
		t.Errorf("branched construction must keep its inc (does not dominate the exit), got 0")
	}
}

// Array literals get the same treatment: an owned rc local consumed as
// an element at its last use is moved into the array (drop_arr_ptr dec's
// it), so the element inc is elided.
func TestMoveOnConstructionElidesIncForArrayElement(t *testing.T) {
	ip := lowerForTest(t, `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var xs: i32[][] = [x];
    return xs[0][0];
}
function main(): i32 { return f(); }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("owned local moved into an array element at last use should emit no __fern_rc_inc, got %d", got)
	}
}

// Composes with move-on-return: `var s = Wrap{inner: x}; return s` moves
// x into s AND moves s out to the caller — zero rc traffic in f.
func TestMoveOnConstructionComposesWithReturn(t *testing.T) {
	ip := lowerForTest(t, `struct Wrap { inner: i32[] }
function f(): Wrap {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s;
}
function main(): i32 { return f().inner[0]; }`)
	f := funcByName(ip, "f")
	if f == nil {
		t.Fatal("no func f")
	}
	if got := incCount(f); got != 0 {
		t.Errorf("move-into-struct then move-on-return should carry zero rc traffic, got %d", got)
	}
}
