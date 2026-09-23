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

import (
	"strings"
	"testing"
)

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
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) {
			checkDynReturnSanitized(t, c.src, c.want)
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

func TestDynReturnedBorrowBounded(t *testing.T) {
	src := dynReturnBumpSrc("500", "2000")
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

// checkDynReturnSanitized runs src under the x86-64 sanitizer: the answer,
// no report, and every allocation freed.
func checkDynReturnSanitized(t *testing.T, src string, want int) {
	t.Helper()
	stdout, stderr, code := runSanitizeX86_64(t, src)
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// A borrow returned through a branch is returned as surely as a bare one:
// an `if` or `match` whose arm yields a parameter, returned directly or
// through a local it initialises. Each arm's yield takes its own unit
// (emitCountedYield), which the caller then owns. On wasm the branch yields
// the two-word `[data, vtable]` pair, and a variant match still frees its
// fresh scrutinee.
func TestDynReturnedBorrowThroughABranch(t *testing.T) {
	const head = `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
`
	const body = `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var x: Box = Box { name: "ab" + "c" };
        var y: Box = Box { name: "d" + "e" };
        var p: dyn Label = pick(i % 2, x, y);
        t = t + p.a() + x.name.len() + y.name.len();
        i = i + 1;
    }
    return t;
}`
	cases := []struct{ name, pick string }{
		{"if_returned", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return if (c == 0) { l } else { m }; }`},
		{"if_bound", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { var r: dyn Label = if (c == 0) { l } else { m }; return r; }`},
		{"match_returned", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return match (c) { 0 => l, _ => m }; }`},
		{"variant_match_returned", `enum Pick { First, Second(i32) }
function tag(c: i32): Pick { if (c == 0) { return Pick.First; } return Pick.Second(c); }
function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return match (tag(c)) { First => l, Second(_) => m }; }`},
	}
	for _, c := range cases {
		src := head + c.pick + body
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) { checkDynReturnSanitized(t, src, 23) })
		t.Run("arm64/"+c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, src); code != 23 {
				t.Errorf("got exit %d, want 23", code)
			}
		})
		t.Run("wasm/"+c.name, func(t *testing.T) {
			if got := runWasm(t, src); got != 23 {
				t.Errorf("got %d, want 23", got)
			}
		})
	}
}
