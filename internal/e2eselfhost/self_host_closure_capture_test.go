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

func TestSelfHostClosureCaptureX86_64(t *testing.T) {
	runBalancedRows(t, "clocap", closureArgBoxCases())
}
