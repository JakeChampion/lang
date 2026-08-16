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

// dynAssignOverwriteBumpSrc reassigns one `dyn` local n times, each RHS a
// fresh concrete owning a heap String. Bounded only if the overwrite releases
// the cell + concrete + String it replaces.
func dynAssignOverwriteBumpSrc(n string) string {
	return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
function main(): i32 {
    var d: dyn Shape = Boxed { tag: "the initial value this loop replaces" };
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        d = Boxed { tag: "a heap string reachable only through the dyn cell" };
        sum = sum + d.area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
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
	t.Run("x86_64", func(t *testing.T) {
		small := mustRunX86_64FreeOn(t, dynAssignOverwriteBumpSrc("50"))
		large := mustRunX86_64FreeOn(t, dynAssignOverwriteBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		small := mustRunArm64FreeOn(t, dynAssignOverwriteBumpSrc("50"))
		large := mustRunArm64FreeOn(t, dynAssignOverwriteBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		small := runWasm(t, dynAssignOverwriteBumpSrc("50"))
		large := runWasm(t, dynAssignOverwriteBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
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
