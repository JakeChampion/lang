// #4380 lever 3: syntactic bounds-check elision for len-bounded loops.
// The parser marks `arr[i]` reads inside `while (i < arr.len())` / C-style
// `for` loops Unchecked (routing to the `_nc` helper) when `0 <= i < len` is
// syntactically provable. These programs must produce identical results with
// the check elided as the interpreter oracle produces with it in place —
// exercising the widened loop shapes beyond the plain while-sum already covered
// by TestArrayInBoundsStillWorks/loop_sum.
package e2e

import "testing"

func TestBoundsElideLenBoundedLoops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bounds-elide e2e in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"for_loop_sum", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [3, 5, 7, 11, 13];
    var s: i32 = 0;
    for (var i: i32 = 0; i < xs.len(); i = i + 1) { s = s + xs[i]; }
    print(s.to_string());
    return 0;
}`},
		{"while_step_two", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [1, 100, 2, 200, 3, 300];
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 2; }
    print(s.to_string());
    return 0;
}`},
		{"nested_if_access", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [4, 9, 2, 7, 6, 1];
    var evens: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] % 2 == 0) { evens = evens + xs[i]; }
        i = i + 1;
    }
    print(evens.to_string());
    return 0;
}`},
		{"nested_loops_same_index_reuse", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [2, 3, 4];
    var ys: i32[] = [10, 20, 30, 40];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { acc = acc + xs[i]; i = i + 1; }
    var j: i32 = 0;
    while (j < ys.len()) { acc = acc + ys[j]; j = j + 1; }
    print(acc.to_string());
    return 0;
}`},
		{"inner_loop_uses_outer_index", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [5, 6, 7];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
        var k: i32 = 0;
        while (k < 3) { acc = acc + xs[i]; k = k + 1; }
        i = i + 1;
    }
    print(acc.to_string());
    return 0;
}`},
		{"find_first_match", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [8, 3, 9, 3, 1];
    var found: i32 = 0 - 1;
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] == 9) { found = i; }
        i = i + 1;
    }
    print(found.to_string());
    return 0;
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { assertNumProgramAgrees(t, c.src) })
	}
}
