// RC of a `dyn Trait` coerced from a struct LOCAL (docs/DYN-TRAITS.md §4.4).
//
// The alias sites decide "this binding owes a retain" from the STATIC type of
// the initialiser, which at a coercion site is the concrete struct — but the
// value the retain lands on is the dyn representation, and neither form
// carries an rc header: the natives box `{data, vtable}` with a plain
// __fern_alloc, and wasm's top word is the static vtable address. The retain
// therefore has to reach the `data` word, which is what `__drop_dyn_<set>`
// releases through the vtable's drop slot.
//
// The two shapes below are the discriminator. At top level the source is at
// its LAST use, so the coercion is a move and no retain is emitted at all —
// that shape was always correct. Inside a loop body the move analysis does not
// fire, the retain is emitted, and both `s` and the aliasing `d` are reclaimed
// per iteration: the retain is what keeps the concrete alive for the second
// of the two drops.
package e2e

import "testing"

// dynCoerceLocalSrc: n iterations of `var d: dyn Shape = s`, with `s` read
// again after the coercion so the source local stays live. Returns the
// accumulated area, so a wrong ANSWER (not just a crash) fails the test.
// Each iteration contributes side*side + side = 9 + 3.
func dynCoerceLocalSrc(n string) string {
	return `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var s: Square = Square { side: 3 };
        var d: dyn Shape = s;
        acc = acc + d.area() + s.side;
        i = i + 1;
    }
    return acc;
}`
}

// dynCoerceLocalNoLoopSrc: the same coercion at top level, where the source is
// at its last use and the coercion is a move. The control for the loop cases.
const dynCoerceLocalNoLoopSrc = `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    var s: Square = Square { side: 3 };
    var d: dyn Shape = s;
    return d.area();
}`

// dynCoerceLocalBumpSrc: the retain added at the coercion must be balanced by
// the dyn drop, so the loop's heap high-water stays flat across n.
func dynCoerceLocalBumpSrc(n string) string {
	return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var s: Boxed = Boxed { tag: "a heap string owned by the concrete behind dyn" };
        var d: dyn Shape = s;
        sum = sum + d.area() + s.tag.len();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// TestDynCoerceFromStructLocalValue: one iteration already computed the WRONG
// value (the stray inc bumped the four bytes preceding the cell, which is the
// neighbouring concrete's payload), and two or more segfaulted once the
// double-freed block was recycled. The no-loop control passed throughout, so
// it separates the coercion itself from the loop-scope reclaim.
func TestDynCoerceFromStructLocalValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"no_loop", dynCoerceLocalNoLoopSrc, 9},
		{"loop_1", dynCoerceLocalSrc("1"), 12},
		{"loop_2", dynCoerceLocalSrc("2"), 24},
		{"loop_5", dynCoerceLocalSrc("5"), 60},
	}
	for _, c := range cases {
		t.Run("x86_64/"+c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
		t.Run("arm64/"+c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
		t.Run("wasm/"+c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestDynCoerceFromStructLocalBounded: the coercion retain must not turn into
// a leak — the concrete (and the String it owns) still reclaim once per
// iteration, so the high-water is the same at n=50 and n=5000.
func TestDynCoerceFromStructLocalBounded(t *testing.T) {
	t.Run("x86_64", func(t *testing.T) {
		small := mustRunX86_64FreeOn(t, dynCoerceLocalBumpSrc("50"))
		large := mustRunX86_64FreeOn(t, dynCoerceLocalBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		small := mustRunArm64FreeOn(t, dynCoerceLocalBumpSrc("50"))
		large := mustRunArm64FreeOn(t, dynCoerceLocalBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		small := runWasm(t, dynCoerceLocalBumpSrc("50"))
		large := runWasm(t, dynCoerceLocalBumpSrc("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
}
