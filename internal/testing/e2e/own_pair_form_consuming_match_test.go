package e2e

import "testing"

func TestOwnPairFormConsumingMatchDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping own pair-form consuming-match differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"aliased_local_pair_form", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    let b: Box = Full([1, 2, 3]);
    let keep: Box = b;
    b = put(b, 0, 9);
    match (keep) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
		{"aliased_in_array_pair_form", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    let b: Box = Full([1, 2, 3]);
    let holder: Box[] = [b];
    b = put(b, 0, 9);
    match (holder[0]) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
		{"unaliased_pair_form_updates_in_place", `import "std/i32";
enum Box { Full(i32[]), Empty }
@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}
function main(): i32 {
    let b: Box = Full([1, 2, 3]);
    b = put(b, 0, 9);
    match (b) { Full(xs) => { print((10 + xs[0]).to_string()); }, Empty => { print("1"); } }
    return 0;
}`},
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
    let b: Box3 = Full([1, 2, 3]);
    let keep: Box3 = b;
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

const ownPairFormConsumingMatchLeakBoundProgram = `enum Box { Full(i32[]), Empty }

@noinline
function put(own b: Box, i: i32, x: i32): Box {
    match (b) { Full(xs) => { return Full(xs.with(i, x)); }, Empty => { return Empty; } }
}

function main(): i32 {
    let b: Box = Full([1, 2, 3]);
    let w: i32 = 0;
    while (w < 200) { b = put(b, 0, w); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { b = put(b, 0, i); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
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
