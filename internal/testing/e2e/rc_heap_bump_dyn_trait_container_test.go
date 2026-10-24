package e2e

import (
	"testing"
)

func dynShapeArraySrc(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
struct Rect   { label: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
impl Shape for Rect   { function area(self: Self): i32 { return 2; } }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let shapes: dyn Shape[] = [
            Circle { tag: "a heap string owned by a circle behind dyn" },
            Rect   { label: "another heap string owned by a rect behind dyn" }
        ];
        sum = sum + shapes[0].area() + shapes[1].area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func dynShapeArrayUnderflowSrc(n string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
struct Rect   { label: string }
impl Shape for Circle { function area(self: Self): i32 { return 1; } }
impl Shape for Rect   { function area(self: Self): i32 { return 2; } }
function main(): i32 {
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let shapes: dyn Shape[] = [
            Circle { tag: "a heap string owned by a circle behind dyn" },
            Rect   { label: "another heap string owned by a rect behind dyn" }
        ];
        sum = sum + shapes[0].area() + shapes[1].area();
        i = i + 1;
    }
    return __rc_underflow_count();
}`
}

func TestX86_64DynShapeArrayHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynShapeArraySrc("50"))
	large := mustRunX86_64FreeOn(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestX86_64DynShapeArrayNoUnderflow(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, dynShapeArrayUnderflowSrc("200")); code != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", code)
	}
}

func TestArm64DynShapeArrayHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynShapeArraySrc("50"))
	large := mustRunArm64FreeOn(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
}

func TestArm64DynShapeArrayNoUnderflow(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, dynShapeArrayUnderflowSrc("200")); code != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", code)
	}
}

func dynMultiTraitArraySrc(n string) string {
	return `import "std/i32";
trait A { function a1(self: Self): i32; }
trait B { function b1(self: Self): i32; function b2(self: Self): i32; }
struct Both  { tag: string }
struct Other { name: string }
impl A for Both  { function a1(self: Self): i32 { return 1; } }
impl B for Both  { function b1(self: Self): i32 { return 2; } function b2(self: Self): i32 { return 3; } }
impl A for Other { function a1(self: Self): i32 { return 4; } }
impl B for Other { function b1(self: Self): i32 { return 5; } function b2(self: Self): i32 { return 6; } }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        let xs: dyn A + B[] = [
            Both  { tag: "a string owned by Both behind a multi-trait dyn" },
            Other { name: "a string owned by Other behind a multi-trait dyn" }
        ];
        sum = sum + xs[0].a1() + xs[1].b2();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64DynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, dynMultiTraitArraySrc("50"))
	large := mustRunX86_64FreeOn(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64DynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, dynMultiTraitArraySrc("50"))
	large := mustRunArm64FreeOn(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMDynShapeArrayHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynShapeArraySrc("50"))
	large := runWasm(t, dynShapeArraySrc("5000"))
	if small != large {
		t.Errorf("dyn Shape[] bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}

func TestWASMDynShapeArrayNoUnderflow(t *testing.T) {
	if got := runWasm(t, dynShapeArrayUnderflowSrc("200")); got != 0 {
		t.Errorf("dyn Shape[] over-releases = %d, want 0", got)
	}
}

func TestWASMDynMultiTraitArrayHeapBumpBounded(t *testing.T) {
	small := runWasm(t, dynMultiTraitArraySrc("50"))
	large := runWasm(t, dynMultiTraitArraySrc("5000"))
	if small != large {
		t.Errorf("dyn A + B[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
}

func dynShapeArrayExitSrc(n, tail string) string {
	return `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { tag: string }
struct Rect   { label: string }
impl Shape for Circle { function area(self: Self): i32 { return self.tag.len(); } }
impl Shape for Rect   { function area(self: Self): i32 { return self.label.len(); } }
function measure(s: dyn Shape): i32 { return s.area(); }
function keep(s: dyn Shape): dyn Shape { return s; }
function build(i: i32): i32 {
    let shapes: dyn Shape[] = [
        Circle { tag: "circle " + (i % 10).to_string() },
        Rect   { label: "rect " + (i % 10).to_string() }
    ];
    let t: i32 = 0;
    for s in shapes { t = t + measure(s); }
    let k = keep(shapes[0]);
    let second: dyn Shape = shapes[1];
    return t + k.area() + second.area();
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < ` + n + `) {
        sum = sum + build(i);
        i = i + 1;
    }
    return ` + tail + `;
}`
}

func TestWASMDynShapeArrayExitBounded(t *testing.T) {
	bump := "(__heap_bump_bytes() as i32) - before"
	small := runWasm(t, dynShapeArrayExitSrc("50", bump))
	large := runWasm(t, dynShapeArrayExitSrc("5000", bump))
	if small != large {
		t.Errorf("function-local dyn Shape[] bump growth should be bounded: N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if got := runWasm(t, dynShapeArrayExitSrc("200", "__rc_underflow_count()")); got != 0 {
		t.Errorf("function-local dyn Shape[] over-releases = %d, want 0", got)
	}
}
