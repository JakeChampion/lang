// Differential regression for #9901: a consuming (`own`-param) match arm whose
// body returns a same-enum construction is reuse-paired (C2), which hands the
// arm's box release AND its shared-branch payload retains to the reuse token
// inside that construction. A PAIR-FORM return builds no box, so there is no
// token: both were emitted nowhere at all. The payload binding was then an
// uncounted alias of a box another binding still named, and the `.with` on it
// read rc 1 and wrote through — `keep` observed the write.
//
// The oracle is the AST interpreter, which always answered correctly; every
// compiled backend disagreed with it. Each case prints its result so all four
// legs are compared by stdout.
package e2e

import "testing"

func TestOwnPairFormConsumingMatchDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping own pair-form consuming-match differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		// The repro: `keep` names the box across the consuming call, so the
		// write must not reach it. interp = 11; compiled was 19 (= 10 + the 9
		// `put` wrote through the alias).
		{"aliased_local_pair_form", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    var keep: Box = b;
    b = put(b, 0, 9);
    match (keep) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
		// The alias held by a container rather than a bare local, so the box's
		// second reference is one a construction counted.
		{"aliased_in_array_pair_form", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    var holder: Box[] = [b];
    b = put(b, 0, 9);
    match (holder[0]) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
		// Guard: with no alias the box IS unique, so the in-place update is
		// sound and must still be taken — the answer is the written 9, on
		// every leg. A fix that made the arm copy unconditionally passes the
		// two cases above and fails here.
		{"unaliased_pair_form_updates_in_place", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    b = put(b, 0, 9);
    match (b) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
		// Guard: the same shape on an enum too wide for the pair form, where
		// the construction really does build a box and the reuse token really
		// does carry the retains. This leg was correct before the fix and
		// pins that the decline is narrow.
		{"aliased_local_boxed_enum", `import "std/i32";
enum Box3 { Full(i32[]), Half(i32[]), Empty }
@noinline
function put3(own b: Box3, i: i32, x: i32): Box3 {
    match (b) {
        Full(xs) => { return Full(xs.with(i, x)); },
        Half(ys) => { return Half(ys.with(i, x)); },
        Empty => { return Empty; }
    }
}
function main(): i32 {
    var b: Box3 = Full([1, 2, 3]);
    var keep: Box3 = b;
    b = put3(b, 0, 9);
    match (keep) {
        Full(xs) => { print((10 + xs[0]).to_string()); },
        Half(ys) => { print("2"); },
        Empty => { print("1"); }
    }
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}

// ownPairFormConsumingMatchLeakBoundProgram pins the other half of #9901: the
// pairing also took the arm's box release with it, so a pair-form consuming
// traversal leaked one box per call. 5000 iterations must stay heap-flat
// (< 512 B growth) with no rc underflow. Exit 98 is what the unfixed lowering
// gives.
const ownPairFormConsumingMatchLeakBoundProgram = `enum Box { Full(i32[]), Empty }

@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}

function main(): i32 {
    var b: Box = Full([1, 2, 3]);
    var w: i32 = 0;
    while (w < 200) { b = put(b, 0, w); w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { b = put(b, 0, i); i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}
`

func TestX86_64OwnPairFormConsumingMatchLeakBound(t *testing.T) {
	if _, code := compileAndRunX86_64(t, ownPairFormConsumingMatchLeakBoundProgram); code != 0 {
		t.Errorf("x86-64 own pair-form consuming-match leak bound: exit = %d, want 0 (98 = the arm leaked its box; 99 = over-release)", code)
	}
}
