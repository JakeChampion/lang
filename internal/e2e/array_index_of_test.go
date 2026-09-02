package e2e

import "testing"

// `xs.index_of(target)` returns `Option[i32]` — `Some(i)` for the first element
// equal to `target`, `None` when absent (#4387). It used to return an `i32`
// `-1` sentinel while `core/cmp`'s free `index_of` returned an Option, which is
// the same-name-different-contract pair #4387 opened on.
//
// The receiver shapes matter more than the values here. On the self-host IR
// path an `i32[]` receiver rides a lowering intercept to a runtime helper
// (`__fern_arr_i32_index_of_opt`) rather than the stdlib body, and the intercept
// keys on the receiver: a LOCAL, a struct FIELD (the #6784 shape, which also
// needs `builtin_arr_opt_ret_type` to recover the scrutinee type of an inline
// `match`), and a `string[]` receiver — which is NOT intercepted and goes
// through the stdlib generic — are three different lowerings of one call.
// `.contains` is checked alongside because it still reads the raw `-1` scan,
// so a change that conflated the two helpers would show up here.
const arrayIndexOfProg = `import "std/array";

struct H { xs: i32[] }

function main(): i32 {
    var xs: i32[] = [7, 8, 9];
    var ss: string[] = ["a", "b", "c"];
    var h: H = H { xs: [7, 8, 9] };

    // local i32[] receiver, hit and miss
    match (xs.index_of(9))  { Some(i) => { if (i != 2) { return 1; } }, None => { return 2; } }
    match (xs.index_of(99)) { Some(_) => { return 3; },                 None => {} }
    // struct-field receiver, inline match (scrutinee type via the builtin registry)
    match (h.xs.index_of(9))  { Some(i) => { if (i != 2) { return 4; } }, None => { return 5; } }
    match (h.xs.index_of(99)) { Some(_) => { return 6; },                 None => {} }
    // string[] receiver — the stdlib generic, not the intercept
    match (ss.index_of("c")) { Some(i) => { if (i != 2) { return 7; } }, None => { return 8; } }
    match (ss.index_of("z")) { Some(_) => { return 9; },                 None => {} }
    // first match wins on a duplicate, and an empty array is None
    var dup: i32[] = [5, 3, 5];
    match (dup.index_of(5)) { Some(i) => { if (i != 0) { return 10; } }, None => { return 11; } }
    var empty: i32[] = [];
    match (empty.index_of(0)) { Some(_) => { return 12; }, None => {} }
    // bound to a local first — the non-inline scrutinee path
    var o: Option[i32] = xs.index_of(8);
    match (o) { Some(i) => { if (i != 1) { return 13; } }, None => { return 14; } }
    // .contains still rides the raw -1 scan on the same receivers
    if (!xs.contains(8))   { return 15; }
    if (xs.contains(88))   { return 16; }
    if (!h.xs.contains(8)) { return 17; }
    return 42;
}
`

// TestNativeArrayIndexOf runs the shipped std/array on interp / x86-64 / wasm.
func TestNativeArrayIndexOf(t *testing.T) {
	p := writeIterProg(t, arrayIndexOfProg)
	if _, code := runFixtureInterp(t, p, ""); code != 42 {
		t.Errorf("array index_of interp = %d, want 42", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 42 {
		t.Errorf("array index_of x86-64 = %d, want 42", code)
	}
	if code := runWasm(t, arrayIndexOfProg); code != 42 {
		t.Errorf("array index_of wasm = %d, want 42", code)
	}
}
