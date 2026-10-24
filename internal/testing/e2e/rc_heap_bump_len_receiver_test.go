package e2e

import (
	"testing"
)

func lenRecvBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "hello there friend, ";
    let b: string = "general kenobi!!!";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + (a + b).len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const lenRecvUnderflowSrc = `function main(): i32 {
    let a: string = "hello there friend, ";
    let b: string = "general kenobi!!!";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        acc = acc + (a + b).len();
        i = i + 1;
    }
    if (acc != 7400) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64LenReceiverReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, lenRecvUnderflowSrc); code != 0 {
		t.Errorf("len-receiver reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64LenReceiverReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, lenRecvUnderflowSrc); code != 0 {
		t.Errorf("len-receiver reclaim: code=%d", code)
	}
}

func TestWASMLenReceiverReclaim(t *testing.T) {
	small := runWasm(t, lenRecvBumpSrc("5000"))
	large := runWasm(t, lenRecvBumpSrc("50000"))
	if small != large {
		t.Errorf("len-receiver bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, lenRecvUnderflowSrc); got != 0 {
		t.Errorf("len-receiver reclaim: got %d", got)
	}
}

const lenCallRecvNativeTail = `    if (g > 900) { return 119; }
    return g / 8;
}`
const lenCallRecvRawTail = `    return g;
}`

func lenCallRecvStrBumpSrc(n, tail string) string {
	return `function f(k: i32): string {
    let p: string = "abcdefgh";
    return "(func $x" + p + " (param i32) (result i32)" + p + " local.get 0" + p + " i32.const 1" + p + " i32.add)" + p;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i).len(); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
` + tail
}

func lenCallRecvArrBumpSrc(n, tail string) string {
	return `function f(k: i32): i32[] {
    return [k, k + 1, k + 2, k + 3, k + 4, k + 5, k + 6, k + 7, k + 8, k + 9];
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i).len(); i = i + 1; }
    if (acc != ` + n + ` * 10) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
` + tail
}

const lenCallRecvAliasSrc = `@noinline function id(s: string): string { return s; }
@noinline function idarr(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let base: string = "0123456789abcdef" + "-suffix-to-force-heap";
    let arr: i32[] = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100];
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 5000) {
        acc = acc + id(base).len() + idarr(arr).len();
        i = i + 1;
    }
    if (acc != 5000 * (37 + 10)) { return 121; }
    if (base.len() != 37) { return 122; }
    if (arr[9] != 100) { return 123; }
    return __rc_underflow_count();
}`

const lenCallRecvBuiltinSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) { acc = acc + random_bytes(32).len(); i = i + 1; }
    if (acc != 200 * 32) { return 121; }
    return __rc_underflow_count();
}`

func runLenCallReceiverChecks(t *testing.T, run func(*testing.T, string) int, nSmall, nLarge, tail string) {
	t.Helper()
	for _, tc := range []struct {
		name string
		src  func(string, string) string
	}{
		{"str-chain", lenCallRecvStrBumpSrc},
		{"arr", lenCallRecvArrBumpSrc},
	} {
		small := run(t, tc.src(nSmall, tail))
		large := run(t, tc.src(nLarge, tail))
		if small != large {
			t.Errorf("%s call-receiver bump should be bounded: N=%s -> %d, N=%s -> %d", tc.name, nSmall, small, nLarge, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", tc.name)
		}
		if tail == lenCallRecvNativeTail && small >= 119 {
			t.Errorf("%s: growth guard tripped (%d) — the call receiver is leaking again", tc.name, small)
		}
	}
	if code := run(t, lenCallRecvAliasSrc); code != 0 {
		t.Errorf("alias-negative: code=%d (121-123=value corruption, >0=over-release)", code)
	}
	if code := run(t, lenCallRecvBuiltinSrc); code != 0 {
		t.Errorf("builtin-negative: code=%d (121=value mismatch, >0=over-release; a crash here means a builtin's headerless buffer was freed through the rc gate)", code)
	}
}

func TestX86_64LenCallReceiverReclaim(t *testing.T) {
	runLenCallReceiverChecks(t, mustRunX86_64FreeOn, "50", "5000", lenCallRecvNativeTail)
}

func TestArm64LenCallReceiverReclaim(t *testing.T) {
	runLenCallReceiverChecks(t, mustRunArm64FreeOn, "50", "5000", lenCallRecvNativeTail)
}

func TestWASMLenCallReceiverReclaim(t *testing.T) {
	runLenCallReceiverChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	}, "5000", "50000", lenCallRecvRawTail)
}
