package e2e

import (
	"testing"
)

func scalarCapClosureArrSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let a: i32 = i;
        let fs: (() => i32)[] = [(): i32 => { return a + 1; }, (): i32 => { return a + 2; }];
        acc = acc + fs[0]() + fs[1]();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func ptrCapClosureArrSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let xs: i32[] = [i, i + 1, i + 2];
        let fs: (() => i32)[] = [(): i32 => { return xs[0]; }, (): i32 => { return xs[2]; }];
        acc = acc + fs[0]() + fs[1]();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const aliasClosureArrSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let a: i32 = i;
        let fs: (() => i32)[] = [(): i32 => { return a + 1; }, (): i32 => { return a + 2; }];
        let gs: (() => i32)[] = fs;
        acc = acc + fs[0]() + gs[1]();
        i = i + 1;
    }
    // each iter: (a+1)+(a+2) = 2i+3; sum i=0..199 = 2*19900 + 600 = 40400
    if (acc != 40400) { return 999; }
    return __rc_underflow_count();
}`

const sharedElemClosureArrSrc = `function main(): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 200) {
        let xs: i32[] = [i, i + 1, i + 2];
        let f: (() => i32) = (): i32 => { return xs[1]; };
        let fs: (() => i32)[] = [f, f];
        acc = acc + fs[0]() + fs[1]() + f();
        i = i + 1;
    }
    // each iter: 3*(i+1); sum i=0..199 of 3i+3 = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ClosureArrayReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, scalarCapClosureArrSrc("5000"))
	large := mustRunX86_64FreeOn(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	ps := mustRunX86_64FreeOn(t, ptrCapClosureArrSrc("5000"))
	pl := mustRunX86_64FreeOn(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if _, code := compileAndRunX86_64FreeOn(t, aliasClosureArrSrc); code != 0 {
		t.Errorf("aliased closure[]: code=%d (999=value/UAF, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, sharedElemClosureArrSrc); code != 0 {
		t.Errorf("shared-element closure[]: code=%d", code)
	}
}

func TestArm64ClosureArrayReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, scalarCapClosureArrSrc("5000"))
	large := mustRunArm64FreeOn(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	ps := mustRunArm64FreeOn(t, ptrCapClosureArrSrc("5000"))
	pl := mustRunArm64FreeOn(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if _, code := compileAndRunArm64FreeOn(t, aliasClosureArrSrc); code != 0 {
		t.Errorf("aliased closure[]: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, sharedElemClosureArrSrc); code != 0 {
		t.Errorf("shared-element closure[]: code=%d", code)
	}
}

func TestWASMClosureArrayReclaim(t *testing.T) {
	small := runWasm(t, scalarCapClosureArrSrc("5000"))
	large := runWasm(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded (env reclaim): N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates closure pairs; expected a non-zero bounded high-water, got 0")
	}
	ps := runWasm(t, ptrCapClosureArrSrc("5000"))
	pl := runWasm(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if got := runWasm(t, aliasClosureArrSrc); got != 0 {
		t.Errorf("aliased closure[]: got %d (999=value/UAF, >0=over-release)", got)
	}
	if got := runWasm(t, sharedElemClosureArrSrc); got != 0 {
		t.Errorf("shared-element closure[]: got %d", got)
	}
}
