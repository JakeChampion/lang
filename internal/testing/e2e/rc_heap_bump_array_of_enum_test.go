package e2e

import (
	"testing"
)

func arrOfEnumBumpSrc(n string) string {
	return `enum E { Arr(i32[]), Empty }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let xs: E[] = [Arr([i, i + 1, i + 2]), Arr([i + 3])];
        match (xs[0]) {
            Arr(a) => { acc = acc + a[0]; },
            Empty => {},
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const arrOfEnumUnderflowSrc = `enum E { Arr(i32[]), Empty }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let a: i32[] = [i, i + 1, i + 2];
        let xs: E[] = [Arr(a), Empty];
        acc = acc + a[0] + a[2];
        i = i + 1;
    }
    // sum over i of (i + (i+2)) = 2i+2, i=0..199 => 2*19900 + 400 = 40200
    if (acc != 40200) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ArrayOfEnumReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, arrOfEnumBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, arrOfEnumUnderflowSrc); code != 0 {
		t.Errorf("E[] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64ArrayOfEnumReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, arrOfEnumBumpSrc("50"))
	large := mustRunArm64FreeOn(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, arrOfEnumUnderflowSrc); code != 0 {
		t.Errorf("E[] reclaim: code=%d", code)
	}
}

func TestWASMArrayOfEnumReclaim(t *testing.T) {
	small := runWasm(t, arrOfEnumBumpSrc("50"))
	large := runWasm(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, arrOfEnumUnderflowSrc); got != 0 {
		t.Errorf("E[] reclaim: got %d", got)
	}
}
