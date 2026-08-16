// Reassignment-overwrite of a `dyn Trait` local (docs/DYN-TRAITS.md §4.4).
//
// The assignment path releases the old value per rc-tracked SHAPE — array,
// struct/enum, string, tuple, Map — and a `dyn` local matches none of them
// (its cell carries no rc header, which is why the exit sweep and the
// loop-body re-declaration both route it through `__drop_dyn_<set>` instead).
// So `d = …` in a loop orphaned the previous cell AND the concrete behind it,
// once per iteration, unbounded. The `var d = …` re-declaration form was
// already reclaimed; only the reassignment was not.
package e2e

import "testing"

// dynAssignOverwriteBumpSrc reassigns one `dyn` local, each RHS a fresh
// concrete owning a heap String, and reports a VERDICT: 0 when a churn four
// times as long adds no fresh high-water (the overwrite released the cell +
// concrete + String it replaced), 1 when it grows.
//
// The verdict, rather than the byte count, because main()'s value becomes an
// exit code and a byte count is read modulo 256. Measured on the leaking
// build: x86-64 reported 64 at n=50 and 0 at n=5000 — so a two-process
// comparison of raw counts was one unlucky residue away from passing
// vacuously. Two churns inside ONE process keep the numbers whole.
func dynAssignOverwriteBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        d = Boxed { tag: "a heap string reachable only through the dyn cell" };
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
    var d: dyn Shape = Boxed { tag: "the initial value this loop replaces" };
    var sum: i32 = 0;
    var i: i32 = 0;
    var base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    var first: i32 = (__heap_bump_bytes() as i32) - base;
    var mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    var second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    return sum - sum;
}`
}

// dynAssignOverwriteAliasSrc: the same reassignment where the RHS is another
// `dyn` LOCAL. That makes the target a borrowed VIEW of a cell `src` still
// owns, so the overwrite must NOT release it — the control that keeps the
// reclaim above from becoming a double free. 4 × (9 + 9).
const dynAssignOverwriteAliasSrc = `import "core/int";
trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    var src: dyn Shape = Square { side: 3 };
    var d: dyn Shape = Square { side: 2 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        d = src;
        acc = acc + d.area() + src.area();
        i = i + 1;
    }
    return (acc - 72) + __rc_underflow_count();
}`

func TestDynAssignOverwriteBounded(t *testing.T) {
	src := dynAssignOverwriteBumpSrc("500", "2000")
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

func TestDynAssignOverwriteAliasNotReleased(t *testing.T) {
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, dynAssignOverwriteAliasSrc); code != 0 {
			t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, dynAssignOverwriteAliasSrc); code != 0 {
			t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, dynAssignOverwriteAliasSrc); got != 0 {
			t.Errorf("got %d, want 0 (wrong value or rc over-release)", got)
		}
	})
}
