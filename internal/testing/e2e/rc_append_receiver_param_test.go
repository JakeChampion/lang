package e2e

import "testing"

func appendReceiverParamVerdictSrc() string {
	return `@noinline
function acc_i(xs: i32[], s: i32): i32[] { return xs.append(s); }
@noinline
function acc_s(xs: string[], s: string): string[] { return xs.append(s); }
@noinline
function acc_chain(xs: i32[], s: i32): i32[] { return xs.append(s).append(s + 1); }
function rounds(pad: string, n: i32): i32 {
    let i: i32 = 0;
    let t: i32 = 0;
    while (i < n) {
        let ys: i32[] = acc_i([], i);
        let zs: i32[] = acc_i([1, 2], i);
        let ss: string[] = acc_s([pad + "a"], pad + "b");
        let ch: i32[] = acc_chain([], i);
        t = t + ys.len() + zs.len() + ss.len() + ch.len();
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    let pad: string = "wide-payload-";
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(pad, 400);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(pad, 800);
    let b2: i64 = __heap_bump_bytes();
    if (x != 3200) { return 991; }
    if (y != 6400) { return 992; }
    if ((b2 - b1) > (b1 - b0)) { return 1; }
    return __rc_underflow_count();
}`
}

func TestX86_64AppendReceiverParamBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, appendReceiverParamVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("append-receiver-param bump should be bounded (#7867): verdict=%d", code)
	}
}

func TestArm64AppendReceiverParamBounded(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, appendReceiverParamVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("append-receiver-param bump should be bounded (#7867): verdict=%d", code)
	}
}

func TestWASMAppendReceiverParamBounded(t *testing.T) {
	if code := runWasm(t, appendReceiverParamVerdictSrc()); code != bumpVerdictFlat {
		t.Errorf("append-receiver-param bump should be bounded (#7867): verdict=%d", code)
	}
}
