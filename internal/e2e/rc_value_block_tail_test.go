package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A value block `{ stmts; tail }` whose tail names a local yields an alias of
// that local, and the exit sweep still releases the local, so every consumer
// has to retain the tail as it would the bare name (#10435). Without the
// retain `return { var q = …; q }` handed back a freed buffer, and an array
// bound from a block was released by the block local's reinit drop while the
// binding still held it.
func TestValueBlockTailAliasIsRetained(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"issue-10435", `struct P { x: i32, y: i32 }
function mk(j: i32): i32[] { return { var q = [j, j * 2]; q = q.append(3); q }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 2) {
        var c: P[] = { var q = [P { x: 5, y: j }]; q };
        var m = mk(j);
        t = t + c[0].y * 10 + m[2];
        j = j + 1;
    }
    return t;
}`, 16},
		{"every-position", `struct P { x: i32, y: i32 }
struct Box { items: i32[] }
function mk(j: i32): i32[] { return { var q = [j, j * 2]; q = q.append(3); q }; }
function pick(b: boolean, j: i32): i32[] {
    return if (b) { var q = [j, 7]; q = q.append(4); q } else { var r = [j, 8]; r = r.append(5); r };
}
function sum(xs: i32[]): i32 { var s = 0; var i = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var c: P[] = { var q = [P { x: 5, y: j }]; q };
        var m = mk(j);
        var p = pick(j == 1, j);
        var s = sum({ var w = [j, 1]; w = w.append(1); w });
        var bx = Box { items: { var v = [j]; v = v.append(2); v } };
        var nest = [{ var e = [j, 9]; e }];
        var ps = { var inner = { var z = [P { x: j, y: 1 }]; z }; inner };
        t = t + c[0].y * 10 + m[2] + p[2] + s + bx.items[1] + nest[0][1] + ps[0].x;
        j = j + 1;
    }
    return t;
}`, 98},
		{"string-struct-tuple", `struct P { x: i32, y: i32 }
function name(j: i32): string { return { var s = "ab"; s = s + "cd"; s }; }
function pt(j: i32): P { return { var p = P { x: j, y: 2 }; p }; }
function main(): i32 {
    var t = 0;
    var j = 0;
    while (j < 3) {
        var s: string = { var a = name(j); a = a + "e"; a };
        var p = if (j > 0) { var q = pt(j); q } else { var r = P { x: 9, y: 9 }; r };
        var tp = { var u = (j, [j, 1]); u };
        var k = { var w = s; w };
        t = t + s.len() + k.len() + p.x + tp.1[1];
        j = j + 1;
    }
    return t;
}`, 45},
	}
	for _, c := range cases {
		checkBalancedOnEveryBackend(t, c.name, c.src, c.want)
	}
}

// checkBalancedOnEveryBackend checks src's answer against the interpreter,
// then that every backend agrees and that the sanitized natives and the wasm
// census report no finding and a balanced heap.
func checkBalancedOnEveryBackend(t *testing.T, name, src string, want int) {
	t.Helper()
	if got := runInterpByte(t, src); got != want {
		t.Fatalf("%s: interp = %d, want %d", name, got, want)
	}
	assertBackendsAgreeWithInterp(t, name, src)
	t.Run("x86_64-sanitize/"+name, func(t *testing.T) {
		checkSanitizedBalanced(t, src, want, runSanitizeX86_64)
	})
	t.Run("arm64-sanitize/"+name, func(t *testing.T) {
		checkSanitizedBalanced(t, src, want, runSanitizeArm64)
	})
	t.Run("wasm-leakcheck/"+name, func(t *testing.T) {
		stdout, stderr, _ := runLeakCheckWasm(t, src, true)
		if got := strings.TrimSpace(stdout); got != strconv.Itoa(want) {
			t.Fatalf("result=%q, want %d\n%s", got, want, stderr)
		}
		if strings.Contains(stderr, "fern-sanitizer:") {
			t.Errorf("sanitizer finding: %q", stderr)
		}
		allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
		if allocs != frees || live != 0 {
			t.Errorf("got allocs=%d frees=%d live=%d, want balanced / 0", allocs, frees, live)
		}
	})
}

const dynShapesPrelude = `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
struct Rect { w: i32, h: i32, tag: string }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h + self.tag.len(); } }
function measure(d: dyn Shape): i32 { return d.area(); }
function total(xs: dyn Shape[]): i32 { var t = 0; for x in xs { t = t + x.area(); } return t; }
`

// A value coerced to `dyn` is retained through the dyn coercion the checker
// recorded against the coerced node. For a value block that node is the block
// itself, so looking it up on the block's tail missed it, and the retain fell
// through to a flat inc of the word below the dyn cell (#10529).
func TestValueBlockDynCoercionIsRetained(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"issue-10529", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    var s: Square = Square { side: 3 };
    var d: dyn Shape = { var q = s; q };
    return d.area();
}`, 9},
		{"var-init", dynShapesPrelude + `function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s = Square { side: j + 2 };
        var r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        var a: dyn Shape = { var q = s; q };
        var b: dyn Shape = if (j > 0) { var q = r; q } else { var z = Rect { w: 1, h: 1, tag: "z" }; z };
        var c: dyn Shape = match (j) { 0 => { var q = r; q }, _ => r };
        var n: dyn Shape = { var inner = { var q = r; q }; inner };
        var d: dyn Shape = s;
        d = { var q = r; q };
        t = t + a.area() + b.area() + c.area() + n.area() + d.area() + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 123},
		{"call-arg", dynShapesPrelude + `function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s = Square { side: j + 2 };
        var r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + measure({ var q = s; q });
        t = t + measure(if (j > 0) { var q = r; q } else { var z = Rect { w: 1, h: 1, tag: "z" }; z });
        t = t + measure(match (j) { 0 => { var q = r; q }, _ => r });
        t = t + measure({ var inner = { var q = r; q }; inner });
        t = t + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 102},
		{"return", dynShapesPrelude + `function blk(s: Square): dyn Shape { return { var q = s; q }; }
function cond(j: i32, r: Rect): dyn Shape { return if (j > 0) { var q = r; q } else { var z = Rect { w: 1, h: 1, tag: "z" }; z }; }
function pick(j: i32, r: Rect): dyn Shape { return match (j) { 0 => { var q = r; q }, _ => r }; }
function fresh(j: i32): dyn Shape { return { var q = Rect { w: j, h: 2, tag: "x" + "y" }; q }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s = Square { side: j + 2 };
        var r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        var a = blk(s);
        var b = cond(j, r);
        var c = pick(j, r);
        var f = fresh(j);
        t = t + a.area() + b.area() + c.area() + f.area() + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 93},
		{"array-element", dynShapesPrelude + `function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var s = Square { side: j + 2 };
        var r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + total([
            { var q = s; q },
            if (j > 0) { var q = r; q } else { var z = Rect { w: 1, h: 1, tag: "z" }; z },
            match (j) { 0 => { var q = r; q }, _ => r },
            s,
        ]);
        t = t + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 110},
	}
	for _, c := range cases {
		checkBalancedOnEveryBackend(t, c.name, c.src, c.want)
	}
}

// An if- or match-expression yields an owned reference whichever arm runs
// (emitCountedYield retains an aliased arm), so a borrowing consumer has to
// release it: a call argument, a `.len()` receiver, an index read. Every one
// of them used to strand it.
func TestConditionalValueIsReleasedByItsConsumer(t *testing.T) {
	const src = `struct P { x: i32, y: i32 }
function sum(xs: i32[]): i32 { var t: i32 = 0; for x in xs { t = t + x; } return t; }
function slen(s: string): i32 { return s.len(); }
function px(p: P): i32 { return p.x; }
function mk(j: i32): i32[] { return [j, j, j]; }
function via_param(a: i32[], b: i32[], c: boolean): i32 { return sum(if (c) { a } else { b }); }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var a = [j, 5];
        a = a.append(1);
        var p = P { x: j, y: 2 };
        var s = "abcdefghij" + "klmnop";
        t = t + sum(if (j > 1) { [j] } else { [1, j] });
        t = t + sum(if (j > 1) { mk(j) } else { a });
        t = t + sum(match (j) { 0 => a, _ => [j, j] });
        t = t + sum(if (j > 0) { if (j > 1) { a } else { mk(j) } } else { [4] });
        t = t + via_param(a, mk(j), j > 1);
        t = t + px(if (j > 1) { p } else { var q = P { x: 3, y: 3 }; q });
        t = t + slen(if (j > 1) { s } else { s + "x" });
        t = t + (if (j > 1) { a } else { [1, 2] }).len();
        t = t + (if (j > 1) { a } else { mk(j) })[0];
        j = j + 1;
    }
    return t;
}`
	checkBalancedOnEveryBackend(t, "conditional-consumers", src, 130)
	checkBalancedOnEveryBackend(t, "dyn-conditional-args", dynShapesPrelude+`function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var d1: dyn Shape = Square { side: j + 1 };
        var d2: dyn Shape = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + measure(if (j > 0) { d1 } else { d2 });
        t = t + measure(match (j) { 0 => d2, 1 => mkd(j), _ => { var q = d1; q } });
        j = j + 1;
    }
    return t;
}`, 34)
}

// A field read straight off an if-, match- or block value has to resolve the
// struct from the value's type; the IR's owner lookup knew only named shapes
// and refused these with `field access on unresolved struct ""`.
func TestFieldReadOffConditionalValue(t *testing.T) {
	const src = `struct P { x: i32, y: i32 }
function main(): i32 {
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < 3) {
        var p = P { x: j, y: 2 };
        t = t + (match (j) { 0 => p, _ => P { x: 7, y: 1 } }).x;
        t = t + (if (j > 1) { p } else { P { x: 4, y: 5 } }).y;
        t = t + ({ var q = P { x: j, y: 6 }; q }).y;
        j = j + 1;
    }
    return t;
}`
	checkBalancedOnEveryBackend(t, "field-read", src, 44)
}
