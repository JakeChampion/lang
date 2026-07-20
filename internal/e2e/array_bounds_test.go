// Bounds-checking contract: an out-of-bounds array or slice index
// aborts the process on every backend instead of returning garbage.
// Before this, the natives (x86-64 / arm64) silently read past the
// end and returned 0 while the interpreter errored and wasm trapped
// — a three-way divergence the cross-area differential sweep found.
// The codegen backends now abort with exit code 134 (the same trap
// the string-slice helper uses and what wasm's `unreachable`
// produces); the interpreter reports a diagnostic and exits non-zero.
package e2e

import "testing"

// assertAborts compiles + runs src on every available codegen
// backend and asserts each one aborts (non-zero exit) without
// printing anything — i.e. the out-of-bounds access was caught
// before it could yield a value.
func assertAborts(t *testing.T, src string) {
	t.Helper()
	t.Run("x86_64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, src)
		if code == 0 {
			t.Errorf("x86_64 did not abort (exit 0); stdout=%q\nsrc:\n%s", out, src)
		}
		if trimOut(out) != "" {
			t.Errorf("x86_64 printed %q before aborting\nsrc:\n%s", out, src)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64(t, src)
		if code == 0 {
			t.Errorf("arm64 did not abort (exit 0); stdout=%q\nsrc:\n%s", out, src)
		}
		if trimOut(out) != "" {
			t.Errorf("arm64 printed %q before aborting\nsrc:\n%s", out, src)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		comp := buildNumComponent(t, src)
		_, _, code := runComponent(t, comp, runOpts{})
		if code == 0 {
			t.Errorf("wasm did not trap (exit 0)\nsrc:\n%s", src)
		}
	})
}

func TestArrayBoundsCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bounds-check e2e in -short mode")
	}
	oob := []struct {
		name, src string
	}{
		{"read_i32_past_end", `import "std/i32";
function main(): i32 { var xs: i32[] = [10, 20, 30]; print(xs[5].to_string()); return 0; }`},
		{"read_negative", `import "std/i32";
function main(): i32 { var xs: i32[] = [10, 20, 30]; var i: i32 = 0 - 1; print(xs[i].to_string()); return 0; }`},
		{"write_past_end", `import "std/i32";
function main(): i32 { var xs: i32[] = [1, 2, 3]; xs = xs.with(7, 9); return 0; }`},
		{"read_u8_past_end", `import "std/i32";
function main(): i32 { var xs: u8[] = [1, 2, 3]; print((xs[9] as i32).to_string()); return 0; }`},
		{"read_i64_past_end", `import "std/i64";
function main(): i32 { var xs: i64[] = [1, 2, 3]; print(xs[5].to_string()); return 0; }`},
		{"slice_past_end", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = xs[1:3];
    print(s[5].to_string());
    return 0;
}`},
		// Slice CONSTRUCTION bounds (#5419): a[lo:hi] with hi > len,
		// lo > hi, or lo < 0 traps at construction — before this,
		// the compiled backends built the oversized view and the
		// access check (against the view's own len) read past the
		// source.
		{"slice_construct_high_past_end", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var s: i32 = 0;
    for x in xs[0:4] { s = s + x; }
    print(s.to_string());
    return 0;
}`},
		{"slice_construct_high_far_past_end", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var s: i32 = 0;
    for x in xs[0:100] { s = s + x; }
    print(s.to_string());
    return 0;
}`},
		{"slice_construct_reversed", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var s: [i32] = xs[2:1];
    print(s.len().to_string());
    return 0;
}`},
		{"slice_construct_negative_low", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var lo: i32 = 0 - 1;
    var s: [i32] = xs[lo:2];
    print(s.len().to_string());
    return 0;
}`},
		{"slice_construct_half_open_low_past_end", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var s: [i32] = xs[7:];
    print(s.len().to_string());
    return 0;
}`},
		{"subslice_construct_past_end", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = xs[1:4];
    var t: [i32] = s[0:4];
    print(t.len().to_string());
    return 0;
}`},
		{"slice_construct_i64_past_end", `import "std/i32";
import "std/i64";
function main(): i32 {
    var xs: i64[] = [5000000000, 6000000000];
    var s: [i64] = xs[0:5];
    print(s.len().to_string());
    return 0;
}`},
	}
	for _, c := range oob {
		t.Run(c.name, func(t *testing.T) { assertAborts(t, c.src) })
	}
}

// TestArrayInBoundsStillWorks guards against the bounds check
// breaking ordinary in-range indexing (reads, writes, every stride).
func TestArrayInBoundsStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"read_write", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    xs = xs.with(1, 99);
    print(xs[0].to_string());
    print(xs[1].to_string());
    print(xs[2].to_string());
    return 0;
}`},
		{"loop_sum", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { sum = sum + xs[i]; i = i + 1; }
    print(sum.to_string());
    return 0;
}`},
		{"slice_inbounds", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = xs[1:4];
    print(s.len().to_string());
    print(s[0].to_string());
    print(s[2].to_string());
    return 0;
}`},
		// The length boundary stays legal: lo == hi == len constructs
		// an empty slice, and a full-width xs[0:len] is unchanged.
		{"slice_boundary_forms", `import "std/i32";
function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var empty: [i32] = xs[3:3];
    print(empty.len().to_string());
    var full: [i32] = xs[0:3];
    print(full.len().to_string());
    var head: [i32] = xs[:2];
    print(head.len().to_string());
    var tail: [i32] = xs[1:];
    print(tail.len().to_string());
    return 0;
}`},
		{"u8_and_i64", `import "std/i32";
import "std/i64";
function main(): i32 {
    var b: u8[] = [200, 100, 50];
    print((b[0] as i32).to_string());
    var w: i64[] = [5000000000, 6000000000];
    print(w[1].to_string());
    return 0;
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { assertNumProgramAgrees(t, c.src) })
	}
}
