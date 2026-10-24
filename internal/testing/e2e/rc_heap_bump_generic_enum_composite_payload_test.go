package e2e

import (
	"testing"
)

func genEnumCompositeBumpSrc(n string) string {
	return `enum Wrap[T] { W(T[]), Empty }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    let stem: string = "alpha";
    while (i < ` + n + `) {
        let w: Wrap[string] = W([stem + "x", stem + "yy", stem + "zzz"]);
        match (w) {
            W(xs) => { acc = acc + xs.len(); },
            Empty => {},
        }
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const genEnumCompositeUnderflowSrc = `enum Wrap[T] { W(T[]), Empty }
function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let stem: string = "s";
        let a: string[] = [stem + "x", stem + "yy", stem + "zzz"];
        let w: Wrap[string] = W(a);
        acc = acc + a[0].len() + a[2].len();
        match (w) { W(xs) => { acc = acc + xs[1].len(); }, Empty => {} }
        i = i + 1;
    }
    // per iter: len(sx)+len(szzz)+len(syy) = 2+4+3 = 9; sum over 200 = 1800
    if (acc != 1800) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64GenericEnumCompositePayloadReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, genEnumCompositeBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, genEnumCompositeBumpSrc("5000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, genEnumCompositeUnderflowSrc); code != 0 {
		t.Errorf("Wrap[string] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64GenericEnumCompositePayloadReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, genEnumCompositeBumpSrc("50"))
	large := mustRunArm64FreeOn(t, genEnumCompositeBumpSrc("5000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, genEnumCompositeUnderflowSrc); code != 0 {
		t.Errorf("Wrap[string] reclaim: code=%d", code)
	}
}

func TestWASMGenericEnumCompositePayloadReclaim(t *testing.T) {
	small := runWasm(t, genEnumCompositeBumpSrc("5000"))
	large := runWasm(t, genEnumCompositeBumpSrc("50000"))
	if small != large {
		t.Errorf("Wrap[string] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, genEnumCompositeUnderflowSrc); got != 0 {
		t.Errorf("Wrap[string] reclaim: got %d", got)
	}
}
