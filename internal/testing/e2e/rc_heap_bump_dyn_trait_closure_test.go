package e2e

import "testing"

func dynClosureCaptureSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
function make(seed: i32): () => i32 {
    let dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a captured dyn" };
    return (): i32 => { return dc.area(); };
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let f: () => i32 = make(i);
        sum = sum + f();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func dynClosureCaptureUnderflowSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
function make(seed: i32): () => i32 {
    let dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a captured dyn" };
    return (): i32 => { return dc.area(); };
}
function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let f: () => i32 = make(i);
        sum = sum + f();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

func TestX86_64DynClosureCaptureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynClosureCaptureSrc("5000"))
	large := mustRunX86_64FreeOn(t, dynClosureCaptureSrc("50000"))
	if large > small {
		t.Errorf("closure-captured-dyn bump growth should be bounded (reclaim): N=5000 -> %d, N=50000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestX86_64DynClosureCaptureNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynClosureCaptureUnderflowSrc("200")); code != 0 {
		t.Errorf("closure-captured-dyn over-releases = %d, want 0", code)
	}
}

func TestArm64DynClosureCaptureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynClosureCaptureSrc("5000"))
	large := mustRunArm64FreeOn(t, dynClosureCaptureSrc("50000"))
	if large > small {
		t.Errorf("closure-captured-dyn bump growth should be bounded (reclaim): N=5000 -> %d, N=50000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestArm64DynClosureCaptureNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynClosureCaptureUnderflowSrc("200")); code != 0 {
		t.Errorf("closure-captured-dyn over-releases = %d, want 0", code)
	}
}
