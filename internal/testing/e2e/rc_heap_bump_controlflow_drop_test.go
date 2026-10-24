package e2e

import (
	"testing"
)

const cfEarlyReturnSrc = `function helper(seed: i32): i32 {
    let big: i32[] = [seed, seed + 1, seed + 2];
    let r: i32 = 0;
    if (seed >= 0) {
        r = big[0] + big[2];
        if (r > 1000000000) { return r; }
    }
    let junk: i32[] = [9, 9, 9];
    return r + junk[0];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        acc = acc + helper(i);
        i = i + 1;
    }
    // helper(i) = (i)+(i+2)+9 = 2i+11; sum i=0..299 = 2*44850 + 3300 = 93000
    if (acc != 93000) { return 999; }
    return __rc_underflow_count();
}`

const cfLoopThenDeadSrc = `function sumloop(seed: i32): i32 {
    let data: i32[] = [seed, seed + 1, seed + 2, seed + 3];
    let s: i32 = 0;
    let j: i32 = 0;
    while (j < 4) { s = s + data[j]; j = j + 1; }
    let after: i32[] = [1, 1];
    return s + after[0];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        acc = acc + sumloop(i);
        i = i + 1;
    }
    // sumloop(i) = (i)+(i+1)+(i+2)+(i+3) + 1 = 4i+7; sum i=0..299 = 4*44850 + 2100 = 181500
    if (acc != 181500) { return 999; }
    return __rc_underflow_count();
}`

const cfAliasedSrc = `struct Holder { items: i32[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let big: i32[] = [i, i + 1, i + 2];
        let h: Holder = Holder { items: big, n: 0 };
        if (i >= 0) { acc = acc + big[0]; }
        let junk: i32[] = [9, 9, 9];
        acc = acc + h.items[2] + junk[0];
        i = i + 1;
    }
    // big[0]=i, h.items[2]=i+2, junk=9 -> 2i+11; sum i=0..199 = 2*19900 + 2200 = 42000
    if (acc != 42000) { return 999; }
    return __rc_underflow_count();
}`

const cfArgsAliasSrc = `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let av: string[] = ["aaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbb", "ccccccccccccccccc"];
        let entry: string = av[0];
        let root: string = "";
        if (av.len() >= 3) { root = av[2]; }   // av's last use is nested
        let junk: string[] = ["ddddddddddddddddd", "eeeeeeeeeeeeeeeee"];
        acc = acc + entry.len() + root.len() + junk[0].len();
        i = i + 1;
    }
    // each string 17 chars: entry + root + junk[0] = 51 per iter * 200 = 10200
    if (acc != 10200) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ControlFlowDrop(t *testing.T) {
	for name, src := range map[string]string{"early-return": cfEarlyReturnSrc, "loop-then-dead": cfLoopThenDeadSrc, "aliased": cfAliasedSrc, "args-alias": cfArgsAliasSrc} {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d", name, code)
		}
	}
}

func TestArm64ControlFlowDrop(t *testing.T) {
	for name, src := range map[string]string{"early-return": cfEarlyReturnSrc, "loop-then-dead": cfLoopThenDeadSrc, "aliased": cfAliasedSrc, "args-alias": cfArgsAliasSrc} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d", name, code)
		}
	}
}
