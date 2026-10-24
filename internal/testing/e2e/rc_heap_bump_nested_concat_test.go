package e2e

import (
	"testing"
)

func nestedConcatBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "longer_string_one_here";
    let b: string = "longer_string_two_here";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + (a + b + a + b).len();
        i = i + 1;
    }
    return ((__heap_bump_bytes() as i32) - before) + (acc - acc);
}`
}

const nestedConcatUnderflowSrc = `function main(): i32 {
    let a: string = "ab";
    let b: string = "cd";
    let c: string = "ef";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let s: string = a + b + c + a; // "abcdefab", len 8
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 1600) { return 999; } // 8 * 200
    let t: string = a + b + c;       // content check
    if (t != "abcdef") { return 888; }
    return __rc_underflow_count();
}`

func TestX86_64NestedConcatReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, nestedConcatBumpSrc("5000"))
	large := mustRunX86_64FreeOn(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedConcatUnderflowSrc); code != 0 {
		t.Errorf("nested-concat reclaim: code=%d (999=len, 888=content, >0=over-release)", code)
	}
}

func TestArm64NestedConcatReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, nestedConcatBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedConcatUnderflowSrc); code != 0 {
		t.Errorf("nested-concat reclaim: code=%d", code)
	}
}

func TestWASMNestedConcatReclaim(t *testing.T) {
	small := runWasm(t, nestedConcatBumpSrc("5000"))
	large := runWasm(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedConcatUnderflowSrc); got != 0 {
		t.Errorf("nested-concat reclaim: got %d", got)
	}
}
