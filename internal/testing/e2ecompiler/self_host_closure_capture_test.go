package e2ecompiler

import "testing"

func closureArgBoxCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "lambda_argument",
			src: `@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    let keep: i32[] = [5 + i, 6];
    return apply((j: i32): i32 => j + keep[1], i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
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
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 3) {
        let keep: i32[] = [i, k, 7];
        let more: i32[] = [k * 2];
        t = t + apply2(k, (j: i32): i32 => j + keep[2] + more[0]);
        k = k + 1;
    }
    return t;
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 75,
		},
	}
}

func closureCaptureCreditCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "captured_strarr_local",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    let keep: string[] = [w(i), w(i + 1)];
    let f = (j: i32): i32 => j + keep[1].len();
    return usr(f, i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 63,
		},
		{
			name: "captured_arrarr_local",
			src: `@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    let keep: i32[][] = [[5 + i], [6], [7]];
    let f = (j: i32): i32 => j + keep[1][0] + keep.len();
    return usr(f, i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 40,
		},
		{
			name: "captured_strarr_param",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(keep: string[], i: i32): i32 {
    let f = (j: i32): i32 => j + keep[0].len();
    return usr(f, i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: string[] = [w(i), w(i + 1)];
        x = x + round(keep, i);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 61,
		},
		{
			name: "lambda_argument_captures_heap",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function apply(f: (i32) => i32, i: i32): i32 { return f(i); }
@noinline
function round(i: i32): i32 {
    let s: string = w(i);
    let rows: i32[][] = [[i], [2]];
    return apply((j: i32): i32 => j + s.len() + rows[1][0], i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 12,
		},
		{
			name: "captured_arrarr_param_row_returned",
			src: `@noinline
function usr(f: (i32) => i32[], i: i32): i32 {
    let g: i32[] = f(i);
    return g.len() + g[0];
}
@noinline
function round(keep: i32[][], i: i32): i32 {
    let f = (j: i32): i32[] => keep[0];
    return usr(f, i);
}
function main(): i32 {
    let keep: i32[][] = [[5], [6], [7]];
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(keep, i); i = i + 1; }
    if (keep[1][0] != 6) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 19,
		},
	}
}

func fnValueArrResultCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "fn_value_result_read",
			src: `@noinline
function usr(f: (i32) => i32[], i: i32): i32 {
    return f(i).len() + f(i)[1];
}
@noinline
function keepref(keep: i32[], i: i32): i32 { return usr((j: i32): i32[] => keep, i); }
function main(): i32 {
    let keep: i32[] = [7, 8];
    let x: i32 = 0;
    let i: i32 = 0;
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
    let keep: i32[] = [7, 8];
    let x: i32 = 0;
    let i: i32 = 0;
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

func closureOwnedCaptureCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "returned_closure_array_capture",
			src: `@noinline
function mk(i: i32): (i32) => i32 {
    let keep: i32[] = [i, i + 1];
    let f = (j: i32): i32 => keep[j];
    return f;
}
@noinline
function churn(i: i32): i32 { let z: i32[] = [i * 7, i * 9]; return z[0] + z[1]; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let g = mk(i);
        let t: i32[] = [555, 666];
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
			name: "returned_closure_heap_captures",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function mk(i: i32): (i32) => i32 {
    let names: string[] = [w(i), w(i + 1)];
    let rows: i32[][] = [[i], [i + 1]];
    let tag: string = w(i + 2);
    return (j: i32): i32 => names[j].len() + rows[j][0] + tag.len();
}
@noinline
function churn(i: i32): i32 { let z: string[] = [w(i * 7), w(i * 9)]; return z[0].len() + z[1].len(); }
function main(): i32 {
    let bad: i32 = 0;
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let g = mk(i);
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
			name: "returned_closure_usize_capture",
			src:  closureUsizeCaptureSrc,
			want: 177,
		},
		{
			name: "returned_closures_in_one_loop",
			src: `function array_capture(n: i32): (i32) => i32 {
    let xs: i32[] = [n, n + 1, n + 2];
    return (x: i32): i32 => { return x + xs[0] + xs[2]; };
}
function two_captures(n: i32): (i32) => i32 {
    let xs: i32[] = [n, n];
    let ys: i32[] = [n, n];
    return (x: i32): i32 => { return x + xs[0] + ys[1]; };
}
function churn(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let f: (i32) => i32 = array_capture(i);
        let g: (i32) => i32 = two_captures(i);
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

func closureInStructCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
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
    let s: string = w(i);
    let xs: i32[] = [i, 2];
    let a: i32 = xs[1];
    let f = (j: i32): i32 => j + s.len();
    let b: Box = keepit(f);
    return a + b.f(1);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 59,
		},
		{
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
    let s: string = w(i);
    let xs: i32[] = [i, 2];
    let a: i32 = xs[1];
    let b: Box = keepit((j: i32): i32 => j + s.len());
    return a + b.f(1);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 59,
		},
		{
			name: "struct_holds_closure_capture",
			src: `struct Box { f: (i32) => i32 }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    let xs: i32[] = [i, 2];
    let f = (j: i32): i32 => j + xs[1];
    let b: Box = keepit(f);
    return b.f(1);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 51,
		},
		{
			name: "struct_result_field_called",
			src: `struct Box { f: (i32) => i32 }
@noinline
function keepit(f: (i32) => i32): Box { return Box { f: f }; }
@noinline
function round(i: i32): i32 {
    let xs: i32[] = [i, 2];
    let f = (j: i32): i32 => j + xs[1];
    return keepit(f).f(1);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
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
    let u: usize = n;
    return (x: i32): i32 => x + ((u / 1000000) as i32);
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let g = mk(3000000000 as usize);
        let h = mk(5000000017 as usize);
        t = (t + g(i) % 97 + h(i) % 89) % 1000;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 251 + 100;
}`

func TestSelfHostClosureUsizeCaptureArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", closureUsizeCaptureSrc)); code != 177 {
		t.Errorf("exited %d, want 177 (99 = rc underflow; 139 = a capture retained as an address)", code)
	}
}

func TestSelfHostClosureCaptureX86_64(t *testing.T) {
	cases := append(closureArgBoxCases(), closureCaptureCreditCases()...)
	cases = append(cases, fnValueArrResultCases()...)
	cases = append(cases, closureOwnedCaptureCases()...)
	runBalancedRows(t, "clocap", append(cases, closureInStructCases()...))
}
