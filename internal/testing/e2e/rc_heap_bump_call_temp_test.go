package e2e

import (
	"testing"
)

const callTempConsumerBumpSrc = `function mk(s: string): string { return slice_unchecked(s, 0, 20) + "!"; }
function burn(a: string, n: i32, mode: i32): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < n) {
        if (mode == 0) { acc = acc + ("<" + mk(a)).len(); }
        if (mode == 1) { if (mk(a) == "zzz") { acc = acc + 1; } }
        if (mode == 2) { let c: u8 = mk(a)[0]; acc = acc + (c as i32); }
        if (mode == 3) { acc = acc + (slice_unchecked(mk(a), 0, 3) + "").len(); }
        i = i + 1;
    }
    return acc;
}
function grew(a: string, mode: i32): i32 {
    burn(a, 2000, mode);
    let b1: i32 = (__heap_bump_bytes() as i32);
    burn(a, 20000, mode);
    return (__heap_bump_bytes() as i32) - b1;
}
function main(): i32 {
    let a: string = "longer_string_one_here";
    let m: i32 = 0;
    while (m < 4) {
        if (grew(a, m) != 0) { return m + 1; }
        m = m + 1;
    }
    return 0;
}`

const callTempConsumerUnderflowSrc = `function mk(s: string): string { return slice_unchecked(s, 0, 2) + "!"; }
function pick(a: string, b: string): string {
    if (a.len() > 3) { return a; }
    return b;
}
function main(): i32 {
    let seed: string = "abcdefghij";
    let a: string = slice_unchecked(seed, 0, 8) + "";
    let b: string = "xy";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let s: string = "<" + mk(a) + ">";      // "<ab!>",      len 5
        let t: string = "[" + pick(a, b) + "]"; // "[abcdefgh]", len 10
        acc = acc + s.len() + t.len();
        if (pick(a, b) == "abcdefgh") { acc = acc + 1; }
        let c: u8 = pick(a, b)[0];              // 'a'
        acc = acc + (c as i32);
        acc = acc + (slice_unchecked(pick(a, b), 0, 3) + "").len();
        i = i + 1;
    }
    if (acc != 23200) { return 999; } // 200 * (5 + 10 + 1 + 97 + 3)
    if (a != "abcdefgh") { return 888; }
    if (b != "xy") { return 887; }
    if (("<" + mk(a) + ">") != "<ab!>") { return 777; }
    return __rc_underflow_count();
}`

func TestX86_64CallTempConsumerReclaim(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, callTempConsumerBumpSrc); code != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: code=%d (1=concat 2=compare 3=index 4=slice)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, callTempConsumerUnderflowSrc); code != 0 {
		t.Errorf("call-temp reclaim: code=%d (999=acc, 888/887=borrowed operand freed, 777=content, >0=over-release)", code)
	}
}

func TestArm64CallTempConsumerReclaim(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, callTempConsumerBumpSrc); code != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: code=%d (1=concat 2=compare 3=index 4=slice)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, callTempConsumerUnderflowSrc); code != 0 {
		t.Errorf("call-temp reclaim: code=%d", code)
	}
}

func TestWASMCallTempConsumerReclaim(t *testing.T) {
	if got := runWasm(t, callTempConsumerBumpSrc); got != 0 {
		t.Errorf("call-temp bump should be flat after warm-up: got %d (1=concat 2=compare 3=index 4=slice)", got)
	}
	if got := runWasm(t, callTempConsumerUnderflowSrc); got != 0 {
		t.Errorf("call-temp reclaim: got %d", got)
	}
}
