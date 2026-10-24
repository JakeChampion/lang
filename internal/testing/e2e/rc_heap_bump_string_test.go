package e2e

import (
	"testing"
)

func stringConcatBumpSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: string = "hello";
    let b: string = "world";
    let i: i32 = 0;
    while (i < ` + n + `) {
        let s: string = a + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const stringConcatUnderflowSrc = `function main(): i32 {
    let a: string = "ab";
    let b: string = "cd";
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 800) { return 999; }
    return __rc_underflow_count();
}`

func longStringReinitBumpSrc(n string) string {
	return `function tag(i: i32): string {
    if (i % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) {
        let s: string = "a-fairly-long-heap-string-prefix-" + tag(i);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const longStringReinitUnderflowSrc = `function tag(i: i32): string {
    if (i % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let s: string = "a-fairly-long-heap-string-prefix-" + tag(i);
        acc = acc + s.len();
        i = i + 1;
    }
    // 33-char prefix + "even"(4)/"odd"(3): even i -> 37, odd i -> 36.
    // i=0..199: 100 even (37) + 100 odd (36) = 3700 + 3600 = 7300.
    if (acc != 7300) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64LongStringReinitBounded(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, longStringReinitUnderflowSrc); code != 0 {
		t.Errorf("long-string reinit reclaim: code=%d", code)
	}
}

func TestArm64LongStringReinitBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, longStringReinitBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, longStringReinitBumpSrc("50000"))
	if small != large {
		t.Errorf("arm64 long-string reinit bump should be bounded (slice 5g follow-up): N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, longStringReinitUnderflowSrc); code != 0 {
		t.Errorf("arm64 long-string reinit reclaim: code=%d", code)
	}
}

func TestWASMLongStringReinitBounded(t *testing.T) {
	small := runWasm(t, longStringReinitBumpSrc("5000"))
	large := runWasm(t, longStringReinitBumpSrc("50000"))
	if small != large {
		t.Errorf("long-string reinit bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if got := runWasm(t, longStringReinitUnderflowSrc); got != 0 {
		t.Errorf("long-string reinit reclaim: got %d", got)
	}
}

func TestX86_64StringConcatBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, stringConcatBumpSrc("5000"))
	large := mustRunX86_64FreeOn(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, stringConcatUnderflowSrc); code != 0 {
		t.Errorf("string-concat reclaim: code=%d (999=value mismatch, >0=over-release)", code)
	}
}

func TestArm64StringConcatBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, stringConcatBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, stringConcatUnderflowSrc); code != 0 {
		t.Errorf("string-concat reclaim: code=%d", code)
	}
}

func TestWASMStringConcatBounded(t *testing.T) {
	small := runWasm(t, stringConcatBumpSrc("5000"))
	large := runWasm(t, stringConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("string-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, stringConcatUnderflowSrc); got != 0 {
		t.Errorf("string-concat reclaim: got %d", got)
	}
}
