package e2e

import (
	"testing"
)

func genEnumArrBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let xs: Option[i32[]][] = [Some([i, i + 1, i + 2]), None, Some([i + 3])];
        match (xs[0]) {
            Some(a) => { acc = acc + a[0]; },
            None => {},
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const genEnumArrUnderflowSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let a: i32[] = [i, i + 1, i + 2];
        let xs: Option[i32[]][] = [Some(a), None, Some([i + 5])];
        acc = acc + a[0] + a[2];
        match (xs[2]) { Some(b) => { acc = acc + b[0]; }, None => {} }
        i = i + 1;
    }
    // per iter: i + (i+2) + (i+5) = 3i+7; sum i=0..199 = 3*19900 + 1400 = 61100
    if (acc != 61100) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64GenericEnumArrayReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, genEnumArrBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, genEnumArrUnderflowSrc); code != 0 {
		t.Errorf("Option[T][] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64GenericEnumArrayReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, genEnumArrBumpSrc("50"))
	large := mustRunArm64FreeOn(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, genEnumArrUnderflowSrc); code != 0 {
		t.Errorf("Option[T][] reclaim: code=%d", code)
	}
}

func TestWASMGenericEnumArrayReclaim(t *testing.T) {
	small := runWasm(t, genEnumArrBumpSrc("50"))
	large := runWasm(t, genEnumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("Option[T][] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, genEnumArrUnderflowSrc); got != 0 {
		t.Errorf("Option[T][] reclaim: got %d", got)
	}
}
