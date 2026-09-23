// A dyn value returned by a function that holds it only as a borrow
// (docs/DYN-TRAITS.md §4.4, #10072). `return l` on a dyn parameter handed the
// caller back the very `{data, vtable}` cell it had built for the argument,
// with no retain: two holders then shared one cell nothing counted, and the
// second release freed the concrete out from under the first
// (use-after-free on x86-64, a segfault unsanitized). The return now gives
// the caller a cell and a unit of the concrete of its own.
//
// The primitive half: every concrete behind a dyn must be retainable for
// that retain to exist, and a primitive's value box had no rc header. It
// has one now, and its drop releases a unit rather than freeing the box
// outright.
package e2e

import "testing"

const dynReturnPrelude = `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
impl Label for i32 { function a(self: Self): i32 { return self; } }
impl Label for string { function a(self: Self): i32 { return self.len(); } }
function pass(l: dyn Label): dyn Label { return l; }
function viaview(l: dyn Label): dyn Label { var v: dyn Label = l; return v; }
function other(l: dyn Label): dyn Label { return Box { name: "zz" + "z" }; }
function show(l: dyn Label): i32 { return l.a(); }
`

// dynReturnSrc returns a borrowed dyn value three ways — a parameter, a local
// viewing one, and a callee that returns something else — over a local
// record, a fresh record and a primitive. Each trip contributes
// 2 + 6 + 2 + 3 + i + 3 + 2 = 18 + i.
func dynReturnSrc(n string) string {
	return dynReturnPrelude + `function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    var keep: Box = Box { name: "kept" };
    while (i < ` + n + `) {
        var local: Box = Box { name: "b" + "c" };
        var r: dyn Label = pass(local);
        var p: dyn Label = pass(Box { name: "fresh" + "x" });
        var v: dyn Label = viaview(local);
        var q: dyn Label = other(keep);
        var k: dyn Label = pass(i);
        t = t + r.a() + p.a() + v.a() + q.a() + k.a() + show("lit") + local.name.len();
        i = i + 1;
    }
    return t;
}`
}

// dynReturnBumpSrc churns the same returns, so a leaked cell or box shows as
// high-water that grows with the churn length.
func dynReturnBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        var local: Box = Box { name: "b" + "c" };
        var r: dyn Label = pass(local);
        var p: dyn Label = pass(Box { name: "fresh" + "x" });
        var q: dyn Label = other(keep);
        var k: dyn Label = pass(i);
        sum = sum + r.a() + p.a() + q.a() + k.a() + show(i) + show("lit");
        i = i + 1;
    }
`
	}
	return dynReturnPrelude + `function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    var keep: Box = Box { name: "kept" };
    var base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    var first: i32 = (__heap_bump_bytes() as i32) - base;
    var mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    var second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    return sum - sum;
}`
}

func TestDynReturnedBorrowTakesItsOwnUnit(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"loop_1", dynReturnSrc("1"), 18},
		{"loop_3", dynReturnSrc("3"), 57},
		{"loop_6", dynReturnSrc("6"), 123},
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

// arm64 does not reclaim dyn values (§4.4 slice 4c), so it has no bounded
// leg.
func TestDynReturnedBorrowBounded(t *testing.T) {
	src := dynReturnBumpSrc("500", "2000")
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", got)
		}
	})
}
