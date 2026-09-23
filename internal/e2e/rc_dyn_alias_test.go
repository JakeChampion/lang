// A borrowed dyn value stored where it outlives the borrow (#10073,
// docs/DYN-TRAITS.md §4.5). A dyn parameter written into an array element or
// captured by a closure copied the caller's `{data, vtable}` cell pointer and
// took no retain, so the holder's release freed what the caller still held,
// and `[l, l]` released one cell twice: a use-after-free on x86-64. A dyn
// alias is now retained like every other reference (emitAliasInc →
// emitDynRetain), which is what retired the borrowed-view bookkeeping
// (`dynBorrowedViews`). A struct field holds its unit the same way, so the
// struct's or tuple's drop releases it, and a spread copy or a destructured
// binding retains it (#10082).
package e2e

import "testing"

// dynAliasSrc stores a dyn parameter in a struct field, twice in an array
// literal, and in a local alias. Each trip contributes 2 + 2 + 2 + 2 + 2 = 10.
func dynAliasSrc(n string) string {
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
struct Holder { l: dyn Label }
function hold(l: dyn Label): Holder { return Holder { l: l }; }
function pair(l: dyn Label): dyn Label[] { return [l, l]; }
function alias(l: dyn Label): i32 { var v: dyn Label = l; var w: dyn Label = v; return w.a(); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var local: Box = Box { name: "b" + "c" };
        var h: Holder = hold(local);
        var xs: dyn Label[] = pair(local);
        t = t + h.l.a() + xs[0].a() + xs[1].a() + alias(local) + local.name.len();
        i = i + 1;
    }
    return t;
}`
}

// dynAliasBumpSrc churns every holder — a struct field, an array, a local
// alias, a closure — so a cell one fails to release shows as growing
// high-water.
func dynAliasBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        var local: Box = Box { name: "b" + "c" };
        var h: Holder = hold(local);
        var xs: dyn Label[] = pair(local);
        var f: () => i32 = later(local);
        sum = sum + h.l.a() + xs[1].a() + alias(local) + f();
        i = i + 1;
    }
`
	}
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
struct Holder { l: dyn Label }
function hold(l: dyn Label): Holder { return Holder { l: l }; }
function pair(l: dyn Label): dyn Label[] { return [l, l]; }
function alias(l: dyn Label): i32 { var v: dyn Label = l; var w: dyn Label = v; return w.a(); }
function later(l: dyn Label): () => i32 { return () => l.a(); }
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

func TestDynAliasStoredPastTheBorrow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"loop_1", dynAliasSrc("1"), 10},
		{"loop_4", dynAliasSrc("4"), 40},
		{"loop_10", dynAliasSrc("10"), 100},
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

// A closure that captures a dyn parameter and escapes. wasm dispatches it to
// the wrong answer on main as well (#10075), so it has no leg here. Each trip
// contributes 2 + 2 = 4.
func TestDynCaptureStoredPastTheBorrow(t *testing.T) {
	src := `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
function later(l: dyn Label): () => i32 { return () => l.a(); }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        var local: Box = Box { name: "b" + "c" };
        var f: () => i32 = later(local);
        t = t + f() + local.name.len();
        i = i + 1;
    }
    return t;
}`
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 40 {
			t.Errorf("got exit %d, want 40", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 40 {
			t.Errorf("got exit %d, want 40", code)
		}
	})
}

// Only x86-64 reclaims every holder: arm64 leaks dyn values (§4.4 slice 4c),
// and wasm does not reclaim a closure's dyn capture (§7.8).
func TestDynAliasStoredPastTheBorrowBounded(t *testing.T) {
	src := dynAliasBumpSrc("500", "2000")
	if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
		t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
	}
}

// dynFieldSrc builds structs holding a dyn field every way a field gets one —
// a fresh concrete, a local, a primitive, a parameter through a callee —
// copies one by spread, and destructures a struct and a tuple holding a dyn
// (#10082). Each trip contributes
// 2 + 2 + i + 2 + 2 + 2 + 1 + 3 + 2 + 2 + 1 = 19 + i.
const dynFieldPrelude = `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
impl Label for i32 { function a(self: Self): i32 { return self; } }
struct Holder { l: dyn Label }
struct Two { n: i32, l: dyn Label, s: string }
function hold(l: dyn Label): Holder { return Holder { l: l }; }
function bump(w: Two): Two { return Two { ...w, n: w.n + 1 }; }
`

func dynFieldBody(bound string) string {
	return `    while (i < ` + bound + `) {
        var local: Box = Box { name: "b" + "c" };
        var f: Holder = Holder { l: Box { name: "x" + "y" } };
        var l: Holder = Holder { l: local };
        var p: Holder = Holder { l: i };
        var h: Holder = hold(local);
        var w: Two = Two { n: 0, l: local, s: "k" + "lm" };
        var v: Two = Two { ...w, n: 1 };
        var u: Two = bump(w);
        let Holder { l: d } = hold(local);
        var tp: (dyn Label, i32) = (d, 1);
        let (x, n) = tp;
        t = t + f.l.a() + l.l.a() + p.l.a() + h.l.a() + v.l.a() + u.l.a() + v.n + u.s.len() + d.a() + x.a() + n;
        i = i + 1;
    }
`
}

func dynFieldSrc(n string) string {
	return dynFieldPrelude + `function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
` + dynFieldBody(n) + `    return t;
}`
}

func dynFieldBumpSrc(n, wider string) string {
	return dynFieldPrelude + `function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    var base: i32 = (__heap_bump_bytes() as i32);
` + dynFieldBody(n) + `    var first: i32 = (__heap_bump_bytes() as i32) - base;
    var mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + dynFieldBody(wider) + `    var second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    return t - t;
}`
}

func TestDynFieldReleasedByStructDrop(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"loop_1", dynFieldSrc("1"), 19},
		{"loop_3", dynFieldSrc("3"), 60},
		{"loop_5", dynFieldSrc("5"), 105},
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
func TestDynFieldReleasedByStructDropBounded(t *testing.T) {
	src := dynFieldBumpSrc("500", "2000")
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
