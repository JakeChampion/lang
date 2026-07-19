// Differential regression for #4873: a method that reads its receiver
// struct's array field, `.append`s to it, and returns a NEW struct wrapping
// the result must NOT mutate the ORIGINAL receiver's shared backing buffer in
// place — even when that array was itself built by a prior append-chain. This
// is the struct-WRAPPED sibling of the bare-array #4827 aliasing bug
// (append_alias_differential_test.go): the receiver's array reaches the grow
// helper through a borrowed struct field across a call boundary, and the
// rc==1-in-place fast path is only sound when the caller consumes the receiver
// (self-reassign), not when it keeps the pre-mutation struct alias live.
//
// On the original repro, interp (correct copy-on-write) gave 22 while x86-64
// silently gave 23 (the shared receiver grew to length 3). The intervening RC
// / Perceus reuse work fixed it; this pins the shape so a future reuse
// "optimization" can't quietly reintroduce it. The issue explicitly noted the
// differential suites didn't carry this shape. Each case prints its result so
// the oracle (interp) and all three backends are compared by stdout.
package e2e

import "testing"

func TestStructFieldAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping struct-field append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		// The headline #4873 repro: a GENERIC struct wraps the array, `a` is
		// built via a push-chain, then `a.push(3)` must not grow `a` in place —
		// `a.size()` after must still be 2. before*10+after = 22 (bug: 23).
		{"generic_box_cow_shared", `import "std/i32";
struct GBox[T] { xs: T[], }
function gbox_new[T](): GBox[T] { return GBox { xs: [] }; }
pub function (b: GBox[T]) push[T](x: T): GBox[T] { var ys: T[] = b.xs.append(x); return GBox { xs: ys }; }
pub function (b: GBox[T]) size(): i32 { return b.xs.len(); }
function main(): i32 {
    var a: GBox[i32] = gbox_new();
    a = a.push(1); a = a.push(2);
    var before: i32 = a.size();       // 2
    var c: GBox[i32] = a.push(3);     // must NOT mutate a
    var after: i32 = a.size();        // still 2
    print((before * 10 + after).to_string());
    return 0;
}`},
		// MONOMORPHIC struct, same push-chain build + kept-alive alias — the
		// issue notes genericity isn't required to trip it.
		{"mono_box_cow_shared", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { var ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    var a: Box = box_new();
    a = a.push(1); a = a.push(2);
    var before: i32 = a.size();
    var c: Box = a.push(3);
    var after: i32 = a.size();
    print((before * 10 + after).to_string());
    return 0;
}`},
		// TWO derived boxes kept live off the same shared receiver: `c` and `d`
		// each wrap a fresh [1,2,x]; `a` must stay [1,2]. a=2 c=3 d=3 → 233.
		{"two_derived_live", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { var ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    var a: Box = box_new();
    a = a.push(1); a = a.push(2);
    var c: Box = a.push(3);
    var d: Box = a.push(4);
    print((a.size() * 100 + c.size() * 10 + d.size()).to_string());
    return 0;
}`},
		// The derived box's CONTENTS (not just length) must be the original plus
		// the one new element — reading c's elements proves the copy is correct,
		// not just length-accounted. c = [1,2,3] → 1+2+3=6; a stays [1,2] → 3.
		{"derived_contents_correct", `import "std/i32";
struct Box { xs: i32[], }
function box_new(): Box { return Box { xs: [] }; }
pub function (b: Box) push(x: i32): Box { var ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) sum(): i32 { var s: i32 = 0; var i: i32 = 0; while (i < b.xs.len()) { s = s + b.xs[i]; i = i + 1; } return s; }
function main(): i32 {
    var a: Box = box_new();
    a = a.push(1); a = a.push(2);
    var c: Box = a.push(3);
    print((c.sum() * 10 + a.sum()).to_string());   // 6*10 + 3 = 63
    return 0;
}`},
		// Guard: a LITERAL-built backing array (`Box { xs: [1, 2] }`) already
		// CoW'd correctly per the issue's isolation note — pin it so the
		// fresh-vs-reused distinction stays honoured. before=2 after=2 → 22.
		{"literal_built_guard", `import "std/i32";
struct Box { xs: i32[], }
pub function (b: Box) push(x: i32): Box { var ys: i32[] = b.xs.append(x); return Box { xs: ys }; }
pub function (b: Box) size(): i32 { return b.xs.len(); }
function main(): i32 {
    var a: Box = Box { xs: [1, 2] };
    var before: i32 = a.size();
    var c: Box = a.push(3);
    var after: i32 = a.size();
    print((before * 10 + after).to_string());
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}
