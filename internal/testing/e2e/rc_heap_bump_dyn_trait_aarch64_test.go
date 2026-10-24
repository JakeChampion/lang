package e2e

import (
	"testing"
)

func dynTraitBumpGrowthSrcArm64(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 1; }
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let d: dyn Shape = Boxed { tag: "a heap-allocated string behind dyn" };
        sum = sum + d.area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestArm64DynTraitHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynTraitBumpGrowthSrcArm64("50"))
	large := mustRunArm64FreeOn(t, dynTraitBumpGrowthSrcArm64("5000"))
	if small != large {
		t.Errorf("dyn-trait bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestArm64DynTraitNoUnderflow(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 1; }
}
function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 200) {
        let d: dyn Shape = Boxed { tag: "another heap string for the dyn box" };
        sum = sum + d.area();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
	if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
		t.Errorf("dyn-trait over-releases = %d, want 0", code)
	}
}

func TestArm64DynTraitMultiTraitDrop(t *testing.T) {
	src := func(n string) string {
		return `import "std/i32";
trait A { function a1(self: Self): i32; }
trait B { function b1(self: Self): i32; function b2(self: Self): i32; }
struct Both { tag: string }
impl A for Both { function a1(self: Self): i32 { return 1; } }
impl B for Both {
    function b1(self: Self): i32 { return 2; }
    function b2(self: Self): i32 { return 3; }
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let d: dyn A + B = Both { tag: "string owned behind a multi-trait dyn" };
        sum = sum + d.a1() + d.b1() + d.b2();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := mustRunArm64FreeOn(t, src("50"))
	large := mustRunArm64FreeOn(t, src("5000"))
	if small != large {
		t.Errorf("multi-trait dyn bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynTraitBorrowedParamNoDrop(t *testing.T) {
	mk := func(tail string) string {
		return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Boxed { tag: string }
impl Shape for Boxed {
    function area(self: Self): i32 { return 7; }
}
// s is dispatched-only -> borrowed; the callee must not drop it.
function use_it(s: dyn Shape): i32 {
    return s.area();
}
function main(): i32 {
    ` + tail + `
}`
	}
	underflow := mk(`let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 200) {
        let d: dyn Shape = Boxed { tag: "borrowed dyn param string value" };
        sum = sum + use_it(d);
        i = i + 1;
    }
    return __rc_underflow_count();`)
	if _, code := compileAndRunArm64FreeOn(t, underflow); code != 0 {
		t.Errorf("borrowed dyn param over-releases = %d, want 0", code)
	}
	bumped := func(n string) string {
		return mk(`let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let d: dyn Shape = Boxed { tag: "borrowed dyn param string value" };
        sum = sum + use_it(d);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;`)
	}
	small := mustRunArm64FreeOn(t, bumped("50"))
	large := mustRunArm64FreeOn(t, bumped("5000"))
	if small != large {
		t.Errorf("borrowed-dyn-param bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}
