package e2e

import (
	"testing"
)

const dynVtableParamSrc = `trait Shape {
    function area(self: Self): i32;
    function scaled(self: Self, by: Factor): i32;
}
struct Factor { k: i32 }
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function scaled(self: Self, by: Factor): i32 { return self.side * by.k; }
}
function main(): i32 {
    let f: Factor = Factor { k: 5 };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let d: dyn Shape = Square { side: 3 };
        acc = acc + d.area() + d.scaled(f) + f.k;
        i = i + 1;
    }
    return (acc - 116) + __rc_underflow_count();
}`

func TestDynVtableParamOwnership(t *testing.T) {
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, dynVtableParamSrc); code != 0 {
			t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, dynVtableParamSrc); code != 0 {
			t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, dynVtableParamSrc); got != 0 {
			t.Errorf("got %d, want 0 (wrong value or rc over-release)", got)
		}
	})
}

func dynVtableParamBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        let d: dyn Shape = Boxed { tag: "a heap string owned by a vtable-dispatched receiver" };
        sum = sum + d.area();
        i = i + 1;
    }
`
	}
	return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
function main(): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    let base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    let first: i32 = (__heap_bump_bytes() as i32) - base;
    let mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    let second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    return sum - sum;
}`
}

func TestDynVtableParamBounded(t *testing.T) {
	src := dynVtableParamBumpSrc("500", "2000")
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", got)
		}
	})
}
