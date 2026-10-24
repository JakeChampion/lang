package e2e

import (
	"testing"
)

func arrOfStructBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let g: P[][] = [[P{x: i, y: 1}], [P{x: 2, y: i}]];
        acc = acc + g[0][0].x;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func arrOf3DBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let g: i32[][][] = [[[i, i + 1]], [[i + 2]]];
        acc = acc + g[0][0][1];
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const arrOfRcUnderflowSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let g: P[][] = [[P{x: i, y: 1}, P{x: 7, y: 2}], [P{x: 2, y: i}]];
        let h: i32[][][] = [[[i, i + 1]], [[i + 2, i + 3]]];
        acc = acc + g[0][1].x + g[1][0].y + h[0][0][1] + h[1][0][0];
        i = i + 1;
    }
    // per iter: 7 + i + (i+1) + (i+2) = 3i+10; sum i=0..199 = 3*19900 + 2000 = 61700
    if (acc != 61700) { return 999; }
    return __rc_underflow_count();
}`

func runArrOfRc(t *testing.T, run func(*testing.T, string) int, requireNonZero bool) {
	for name, src := range map[string]func(string) string{"struct[][]": arrOfStructBumpSrc, "i32[][][]": arrOf3DBumpSrc} {
		small := run(t, src("50"))
		large := run(t, src("5000"))
		if small != large {
			t.Errorf("%s bump should be bounded (inner reclaim): N=50 -> %d, N=5000 -> %d", name, small, large)
		}
		if requireNonZero && small == 0 {
			t.Errorf("%s: wasm heap-allocates; expected a non-zero bounded high-water, got 0", name)
		}
	}
}

func TestX86_64ArrayOfRcReclaim(t *testing.T) {
	runArrOfRc(t, mustRunX86_64FreeOn, false)
	if _, code := compileAndRunX86_64FreeOn(t, arrOfRcUnderflowSrc); code != 0 {
		t.Errorf("array-of-rc reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64ArrayOfRcReclaim(t *testing.T) {
	runArrOfRc(t, mustRunArm64FreeOn, false)
	if _, code := compileAndRunArm64FreeOn(t, arrOfRcUnderflowSrc); code != 0 {
		t.Errorf("array-of-rc reclaim: code=%d", code)
	}
}

func TestWASMArrayOfRcReclaim(t *testing.T) {
	runArrOfRc(t, runWasm, true)
	if got := runWasm(t, arrOfRcUnderflowSrc); got != 0 {
		t.Errorf("array-of-rc reclaim: got %d", got)
	}
}
