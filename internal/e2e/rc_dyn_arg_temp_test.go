// A fresh enum temporary coerced to `dyn Trait` at a call argument
// (docs/DYN-TRAITS.md §4.4). The argument is stashed so it can be released
// once the callee has borrowed it, and the stash was typed by the argument's
// STATIC type, the enum, while the value the coercion lowers to is the dyn
// representation. The post-call release therefore ran the enum's drop on the
// natives' `{data, vtable}` cell, which has no rc header: the unique test read
// the eight bytes before the cell, and the tag read took the data pointer as a
// variant tag. Whether that crashed depended on what the allocator had put
// next to the cell — a dyn local declared in the same loop body was enough on
// both x86-64 and arm64.
package e2e

import "testing"

// dynArgTempSrc dispatches through a fresh `Shape.Line(i)` temporary each
// trip, with a counted record behind a dyn local in the same body. Each trip
// contributes 2 + i.
func dynArgTempSrc(n string) string {
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
enum Shape { Dot, Line(i32) }
impl Label for Shape {
    function a(self: Self): i32 {
        match (self) {
            Dot => { return 1; },
            Line(len) => { return 2 + len; }
        }
    }
}
function show(l: dyn Label): i32 { return l.a(); }
function main(): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var b: dyn Label = Box { name: "b" + "c" };
        total = total + show(Shape.Line(i));
        i = i + 1;
    }
    return total;
}`
}

// dynArgTempBumpSrc: the temporary's concrete owns a string, and releasing the
// stash as a dyn has to reach it through the vtable's drop slot, so a churn
// four times as long adds no heap high-water. Returns a verdict for the reason
// rc_dyn_coerce_struct_local_test.go gives.
func dynArgTempBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        sum = sum + show(Tagged.Named("a heap string owned by the enum behind dyn" + i.to_string()));
        i = i + 1;
    }
`
	}
	return `import "std/i32";
trait Label { function a(self: Self): i32; }
enum Tagged { Blank, Named(string) }
impl Label for Tagged {
    function a(self: Self): i32 {
        match (self) {
            Blank => { return 0; },
            Named(s) => { return s.len(); }
        }
    }
}
function show(l: dyn Label): i32 { return l.a(); }
function main(): i32 {
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

func TestDynCoercedArgTempReleasedAsDyn(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"loop_1", dynArgTempSrc("1"), 2},
		{"loop_3", dynArgTempSrc("3"), 9},
		{"loop_10", dynArgTempSrc("10"), 65},
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

// arm64 does not reclaim dyn values (§4.4 slice 4c), so it leaks the
// temporary by design and has no bounded leg.
func TestDynCoercedArgTempBounded(t *testing.T) {
	src := dynArgTempBumpSrc("500", "2000")
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

// dynArgNonFreshSrc coerces concretes the call does not own at the argument:
// a local record, a record from outside the loop, a nullary variant, an
// integer and a string. The natives' `{data, vtable}` cell is still the
// call's own. It is released as a dyn, so the concrete is retained for it
// first and the release balances. Each trip contributes 1 + 2 + 4 + i + 3 +
// the local's name length again, read after the calls.
func dynArgNonFreshSrc(n string) string {
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
enum Shape { Dot, Line(i32) }
impl Label for Shape {
    function a(self: Self): i32 {
        match (self) {
            Dot => { return 1; },
            Line(len) => { return len; }
        }
    }
}
impl Label for i32 { function a(self: Self): i32 { return self; } }
impl Label for string { function a(self: Self): i32 { return self.len(); } }
function show(l: dyn Label): i32 { return l.a(); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    var keep: Box = Box { name: "kept" };
    var s: string = "ab" + "c";
    while (i < ` + n + `) {
        var local: Box = Box { name: "b" + "c" };
        t = t + show(Shape.Dot) + show(local) + show(keep) + show(i) + show(s);
        t = t + local.name.len();
        i = i + 1;
    }
    return t + keep.name.len() + s.len();
}`
}

// dynArgNonFreshBumpSrc churns the same non-fresh coercions, so a leaked cell
// shows as high-water that grows with the churn length.
func dynArgNonFreshBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        var local: Box = Box { name: "b" + "c" };
        sum = sum + show(Shape.Dot) + show(local) + show(keep) + show(i);
        i = i + 1;
    }
`
	}
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
enum Shape { Dot, Line(i32) }
impl Label for Shape {
    function a(self: Self): i32 {
        match (self) {
            Dot => { return 1; },
            Line(len) => { return len; }
        }
    }
}
impl Label for i32 { function a(self: Self): i32 { return self; } }
function show(l: dyn Label): i32 { return l.a(); }
function main(): i32 {
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

func TestDynCoercedNonFreshArgReleasedAsDyn(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"loop_1", dynArgNonFreshSrc("1"), 19},
		{"loop_4", dynArgNonFreshSrc("4"), 61},
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

func TestDynCoercedNonFreshArgBounded(t *testing.T) {
	src := dynArgNonFreshBumpSrc("500", "2000")
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
