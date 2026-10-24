package e2e

import (
	"testing"
)

func structFieldBumpSrc(n string) string {
	return `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let b: Box = Box { data: [i, i + 1, i + 2], tag: i };
        sum = sum + b.data[0] + b.tag;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func enumPayloadBumpSrc(n string) string {
	return `enum E { Arr(i32[]), Empty }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let e: E = Arr([i, i + 1, i + 2]);
        match (e) {
            Arr(xs) => { sum = sum + xs[0]; },
            Empty => { sum = sum + 1; },
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const structEnumUnderflowSrc = `struct Box { data: i32[], tag: i32 }
enum E { Arr(i32[]), Empty }
function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 200) {
        let b: Box = Box { data: [i, i + 1, i + 2], tag: i };
        let e: E = Arr([i, i + 5]);
        match (e) {
            Arr(xs) => { sum = sum + b.data[0] + b.tag + xs[1]; },
            Empty => { sum = sum + 1; },
        }
        i = i + 1;
    }
    if (sum != 60700) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64StructEnumHeapBumpBounded(t *testing.T) {
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := mustRunX86_64FreeOn(t, src("50"))
		large := mustRunX86_64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if _, code := compileAndRunX86_64FreeOn(t, structEnumUnderflowSrc); code != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: code=%d (999=value, >0=over-release)", code)
	}
}

func TestArm64StructEnumHeapBumpBounded(t *testing.T) {
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := mustRunArm64FreeOn(t, src("50"))
		large := mustRunArm64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if _, code := compileAndRunArm64FreeOn(t, structEnumUnderflowSrc); code != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: code=%d", code)
	}
}

func TestWASMStructEnumHeapBumpBounded(t *testing.T) {
	for _, src := range []func(string) string{structFieldBumpSrc, enumPayloadBumpSrc} {
		small := runWasm(t, src("50"))
		large := runWasm(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded (deep reclaim): N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	}
	if got := runWasm(t, structEnumUnderflowSrc); got != 0 {
		t.Errorf("struct/enum reinit over-releases or value mismatch: got %d", got)
	}
}
