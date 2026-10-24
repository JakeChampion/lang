package e2e

import (
	"testing"
)

func enumReusePayloadBumpSrc(n string) string {
	return `enum E { Wrap(i32[]), Empty }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let e: E = Empty;
    let i: i32 = 0;
    while (i < ` + n + `) {
        e = Wrap([i, i + 1, i + 2, i + 3]);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const enumReuseCrossVariantSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    let b: Bag = Keep([0, 0, 0]);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 300) {
        b = Keep([i, i + 1, i + 2]);
        b = Swap([i + 10, i + 11, i + 12]);
        match (b) { Keep(x) => { acc = acc + x[0]; }, Swap(y) => { acc = acc + y[0]; } }
        i = i + 1;
    }
    // each iter ends as Swap([i+10,..]); acc += i+10; sum i=0..299 = 44850 + 3000 = 47850
    if (acc != 47850) { return 999; }
    return __rc_underflow_count();
}`

const enumReuseAliasedPayloadSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let a: i32[] = [i, i + 1, i + 2];
        let b: Bag = Keep(a);
        let junk: i32[] = [99, 99, 99];
        acc = acc + a[0] + a[2] + junk[0];
        i = i + 1;
    }
    // (i)+(i+2)+99 = 2i+101; i=0..199 -> 39800 + 20200 = 60000
    if (acc != 60000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64EnumReusePayloadReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, enumReusePayloadBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, enumReuseCrossVariantSrc); code != 0 {
		t.Errorf("cross-variant reuse: code=%d (999=value/UAF, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, enumReuseAliasedPayloadSrc); code != 0 {
		t.Errorf("aliased payload reuse: code=%d", code)
	}
}

func TestArm64EnumReusePayloadReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, enumReusePayloadBumpSrc("50"))
	large := mustRunArm64FreeOn(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, enumReuseCrossVariantSrc); code != 0 {
		t.Errorf("cross-variant reuse: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, enumReuseAliasedPayloadSrc); code != 0 {
		t.Errorf("aliased payload reuse: code=%d", code)
	}
}

func TestWASMEnumReusePayloadReclaim(t *testing.T) {
	small := runWasm(t, enumReusePayloadBumpSrc("50"))
	large := runWasm(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded (old-payload reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, enumReuseCrossVariantSrc); got != 0 {
		t.Errorf("cross-variant reuse: got %d (999=value/UAF, >0=over-release)", got)
	}
	if got := runWasm(t, enumReuseAliasedPayloadSrc); got != 0 {
		t.Errorf("aliased payload reuse: got %d", got)
	}
}
