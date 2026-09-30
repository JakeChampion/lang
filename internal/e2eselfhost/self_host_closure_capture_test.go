package e2eselfhost

import "testing"

// Capturing closures on the AST lowering. A lambda's env box holds each
// capture as a borrow, and the frame that built the box releases it.

// A capturing lambda written as a call argument (#10746): its box is fresh and
// only the argument holds it, so at a borrowable position the call's own
// release frees it once the callee returns.
func closureArgBoxCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "lambda_argument",
			src: `@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    var keep: i32[] = [5 + i, 6];
    return apply((j: i32): i32 => j + keep[1], i);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 72,
		},
		{
			name: "lambda_argument_in_loop",
			src: `@noinline
function apply2(n: i32, f: (i32) => i32): i32 { return f(n) + f(n + 1); }
@noinline
function round(i: i32): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 3) {
        var keep: i32[] = [i, k, 7];
        var more: i32[] = [k * 2];
        t = t + apply2(k, (j: i32): i32 => j + keep[2] + more[0]);
        k = k + 1;
    }
    return t;
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 75,
		},
	}
}

// A local a closure captures keeps its own release, element walk included
// (#10745). The box is keyed in the borrowability registry like a call, one
// flag per capture, so a capture is a borrow wherever the box dies in the frame
// that built it and the closure body keeps the capture no longer than the box.
func closureCaptureCreditCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			// A string[] local captured by a closure the frame lends to a borrowable position.
			name: "captured_strarr_local",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    var keep: string[] = [w(i), w(i + 1)];
    var f = (j: i32): i32 => j + keep[1].len();
    return usr(f, i);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 63,
		},
		{
			// An i32[][] local captured the same way.
			name: "captured_arrarr_local",
			src: `@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    var keep: i32[][] = [[5 + i], [6], [7]];
    var f = (j: i32): i32 => j + keep[1][0] + keep.len();
    return usr(f, i);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 40,
		},
		{
			// A string[] parameter captured: its caller keeps the element release.
			name: "captured_strarr_param",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(keep: string[], i: i32): i32 {
    var f = (j: i32): i32 => j + keep[0].len();
    return usr(f, i);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var keep: string[] = [w(i), w(i + 1)];
        x = x + round(keep, i);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 61,
		},
		{
			// A heap string and an i32[][] captured by a lambda written as an argument.
			name: "lambda_argument_captures_heap",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    var s: string = w(i);
    var rows: i32[][] = [[i], [2]];
    return apply((j: i32): i32 => j + s.len() + rows[1][0], i);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 12,
		},
		{
			// A captured i32[][] parameter whose closure returns a row (#10748):
			// the index read retains, so the caller keeps its element walk.
			name: "captured_arrarr_param_row_returned",
			src: `@noinline
function usr(f: (i32) => i32[], i: i32): i32 {
    var g: i32[] = f(i);
    return g.len() + g[0];
}
@noinline
function round(keep: i32[][], i: i32): i32 {
    var f = (j: i32): i32[] => keep[0];
    return usr(f, i);
}
function main(): i32 {
    var keep: i32[][] = [[5], [6], [7]];
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(keep, i); i = i + 1; }
    if (keep[1][0] != 6) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 19,
		},
	}
}

// The array result of a call through a function value is a count the caller
// owns (#10754), whether it is bound or only read and dropped.
func fnValueArrResultCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			// Read through `.len()` and an index, from a fresh lambda and one
			// returning its capture.
			name: "fn_value_result_read",
			src: `@noinline
function usr(f: (i32) => i32[], i: i32): i32 {
    return f(i).len() + f(i)[1];
}
@noinline
function keepref(keep: i32[], i: i32): i32 { return usr((j: i32): i32[] => keep, i); }
function main(): i32 {
    var keep: i32[] = [7, 8];
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        x = x + usr((j: i32): i32[] => [j, j + 1], i) + keepref(keep, i);
        i = i + 1;
    }
    if (keep[1] != 8) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 25,
		},
		{
			// Dropped as a statement, from a producer and from a function that
			// returns its argument.
			name: "fn_value_result_dropped",
			src: `@noinline
function pair(j: i32): i32[] { return [j, j * 2]; }
@noinline
function same(xs: i32[]): i32[] { return xs; }
@noinline
function usr(f: (i32) => i32[], i: i32): i32 {
    f(i);
    return 1;
}
@noinline
function over(g: (i32[]) => i32[], xs: i32[]): i32 {
    g(xs);
    return g(xs).len();
}
function main(): i32 {
    var keep: i32[] = [7, 8];
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        x = x + usr(pair, i) + over(same, keep);
        i = i + 1;
    }
    if (keep[1] != 8) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 51,
		},
	}
}

func TestSelfHostClosureCaptureX86_64(t *testing.T) {
	cases := append(closureArgBoxCases(), closureCaptureCreditCases()...)
	runBalancedRows(t, "clocap", append(cases, fnValueArrResultCases()...))
}
