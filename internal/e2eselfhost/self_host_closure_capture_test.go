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

// A closure that outlives the frame building it holds a count of each
// capture and gives it back when it is freed (#9657). The value is checked on
// the leakcheck build, which runs without the sanitizer that hid the fault.
func closureOwnedCaptureCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			// A returned closure reads its captured array after the frame that built it
			// returned, while a fresh allocation could reuse a freed block.
			name: "returned_closure_array_capture",
			src: `@noinline
function mk(i: i32): (i32) => i32 {
    var keep: i32[] = [i, i + 1];
    var f = (j: i32): i32 => keep[j];
    return f;
}
@noinline
function churn(i: i32): i32 { var z: i32[] = [i * 7, i * 9]; return z[0] + z[1]; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var g = mk(i);
        var t: i32[] = [555, 666];
        if (g(1) != i + 1) { bad = bad + 1; }
        if (t[0] != 555) { bad = bad + 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return bad;
}`,
			want: 0,
		},
		{
			// The same over a string[], an i32[][] and a string.
			name: "returned_closure_heap_captures",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function mk(i: i32): (i32) => i32 {
    var names: string[] = [w(i), w(i + 1)];
    var rows: i32[][] = [[i], [i + 1]];
    var tag: string = w(i + 2);
    return (j: i32): i32 => names[j].len() + rows[j][0] + tag.len();
}
@noinline
function churn(i: i32): i32 { var z: string[] = [w(i * 7), w(i * 9)]; return z[0].len() + z[1].len(); }
function main(): i32 {
    var bad: i32 = 0;
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var g = mk(i);
        x = x + churn(i);
        if (g(1) != w(i + 1).len() + i + 1 + w(i + 2).len()) { bad = bad + 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (x < 0) { return 98; }
    return bad;
}`,
			want: 0,
		},
		{
			// A usize capture is a scalar the box holds no count of: past the
			// below-heap guard (256 MiB, and past 4 GiB) a retain read it as an
			// address.
			name: "returned_closure_usize_capture",
			src:  closureUsizeCaptureSrc,
			want: 177,
		},
		{
			// The two-closure shape #9657 was filed with.
			name: "returned_closures_in_one_loop",
			src: `function array_capture(n: i32): (i32) => i32 {
    var xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}
function two_captures(n: i32): (i32) => i32 {
    var xs: i32[] = [n, n];
    var ys: i32[] = [n, n];
    return (x: i32): i32 => { return x + xs[0] + ys[1]; };
}
function churn(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var f: (i32) => i32 = array_capture(i);
        var g: (i32) => i32 = two_captures(i);
        t = t + f(0) % 3 + g(0) % 3;
        i = i + 1;
    }
    return t;
}
function main(): i32 { return churn() % 7; }`,
			want: 3,
		},
	}
}

// A struct holding a closure, built by a call (#10755).
func closureInStructCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			// A closure local stored through a callee into a returned struct, called
			// through the struct's field.
			name: "struct_holds_bound_closure",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
struct Box { f: (i32) => i32 }
@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    var s: string = w(i);
    var xs: i32[] = [i, 2];
    var a: i32 = xs[1];
    var f = (j: i32): i32 => j + s.len();
    var b: Box = keepit(f);
    return a + b.f(1);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 59,
		},
		{
			// The same with the lambda written as the argument.
			name: "struct_holds_lambda_argument",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
struct Box { f: (i32) => i32 }
@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    var s: string = w(i);
    var xs: i32[] = [i, 2];
    var a: i32 = xs[1];
    var b: Box = keepit((j: i32): i32 => j + s.len());
    return a + b.f(1);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 59,
		},
		{
			// A struct bound from the call, over a closure capturing an array.
			name: "struct_holds_closure_capture",
			src: `struct Box { f: (i32) => i32 }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    var xs: i32[] = [i, 2];
    var f = (j: i32): i32 => j + xs[1];
    var b: Box = keepit(f);
    return b.f(1);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 51,
		},
		{
			// The struct result called through its field and dropped.
			name: "struct_result_field_called",
			src: `struct Box { f: (i32) => i32 }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    var xs: i32[] = [i, 2];
    var f = (j: i32): i32 => j + xs[1];
    return keepit(f).f(1);
}
function main(): i32 {
    var x: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 51,
		},
	}
}

const closureUsizeCaptureSrc = `@noinline
function mk(n: usize): (i32) => i32 {
    var u: usize = n;
    return (x: i32): i32 => x + ((u / 1000000) as i32);
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var g = mk(3000000000 as usize);
        var h = mk(5000000017 as usize);
        t = (t + g(i) % 97 + h(i) % 89) % 1000;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 251 + 100;
}`

// TestSelfHostClosureUsizeCaptureArm64: the usize capture row on arm64, both
// lowerings, value and underflow gate (interpreter-confirmed 177; the division
// reads the capture's full width).
func TestSelfHostClosureUsizeCaptureArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, env := range []string{"FERN_SEM_IR=1", "FERN_SEM_IR="} {
		if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", closureUsizeCaptureSrc, env)); code != 177 {
			t.Errorf("%s: exited %d, want 177 (99 = rc underflow; 139 = a capture retained as an address)", env, code)
		}
	}
}

func TestSelfHostClosureCaptureX86_64(t *testing.T) {
	cases := append(closureArgBoxCases(), closureCaptureCreditCases()...)
	cases = append(cases, fnValueArrResultCases()...)
	cases = append(cases, closureOwnedCaptureCases()...)
	runBalancedRows(t, "clocap", append(cases, closureInStructCases()...))
}
