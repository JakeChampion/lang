package e2e

import "testing"

func dynEnumPayloadSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
enum Box { Wrap(dyn Shape), Empty }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a dyn enum payload" };
        let b: Box = Box.Wrap(dc);
        match (b) {
            Wrap(s) => { sum = sum + s.area(); },
            Empty => { sum = sum + 0; },
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func dynEnumPayloadUnderflowSrc(n string) string {
	return `trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
enum Box { Wrap(dyn Shape), Empty }
function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let dc: dyn Shape = Circle { tag: "a heap string owned by a circle behind a dyn enum payload" };
        let b: Box = Box.Wrap(dc);
        match (b) {
            Wrap(s) => { sum = sum + s.area(); },
            Empty => { sum = sum + 0; },
        }
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

func TestX86_64DynEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynEnumPayloadSrc("50"))
	large := mustRunX86_64FreeOn(t, dynEnumPayloadSrc("5000"))
	if small != large {
		t.Errorf("enum-of-dyn bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestX86_64DynEnumPayloadNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynEnumPayloadUnderflowSrc("200")); code != 0 {
		t.Errorf("enum-of-dyn over-releases = %d, want 0", code)
	}
}

func TestArm64DynEnumPayloadHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynEnumPayloadSrc("50"))
	large := mustRunArm64FreeOn(t, dynEnumPayloadSrc("5000"))
	if small != large {
		t.Errorf("enum-of-dyn bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestArm64DynEnumPayloadNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynEnumPayloadUnderflowSrc("200")); code != 0 {
		t.Errorf("enum-of-dyn over-releases = %d, want 0", code)
	}
}
