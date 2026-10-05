package e2e

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"strconv"
	"strings"
	"testing"
)

// A value block `{ stmts; tail }` whose tail names a local yields an alias of
// that local, and the exit sweep still releases the local, so every consumer
// has to retain the tail as it would the bare name (#10435). Without the
// retain `return { let q = …; q }` handed back a freed buffer, and an array
// bound from a block was released by the block local's reinit drop while the
// binding still held it.
func TestValueBlockTailAliasIsRetained(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"issue-10435", `struct P { x: i32, y: i32 }
function mk(j: i32): i32[] { return { let q = [j, j * 2]; q = q.append(3); q }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 2) {
        let c: P[] = { let q = [P { x: 5, y: j }]; q };
        let m = mk(j);
        t = t + c[0].y * 10 + m[2];
        j = j + 1;
    }
    return t;
}`, 16},
		{"every-position", `struct P { x: i32, y: i32 }
struct Box { items: i32[] }
function mk(j: i32): i32[] { return { let q = [j, j * 2]; q = q.append(3); q }; }
function pick(b: boolean, j: i32): i32[] {
    return if (b) { let q = [j, 7]; q = q.append(4); q } else { let r = [j, 8]; r = r.append(5); r };
}
function sum(xs: i32[]): i32 { let s = 0; let i = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let c: P[] = { let q = [P { x: 5, y: j }]; q };
        let m = mk(j);
        let p = pick(j == 1, j);
        let s = sum({ let w = [j, 1]; w = w.append(1); w });
        let bx = Box { items: { let v = [j]; v = v.append(2); v } };
        let nest = [{ let e = [j, 9]; e }];
        let ps = { let inner = { let z = [P { x: j, y: 1 }]; z }; inner };
        t = t + c[0].y * 10 + m[2] + p[2] + s + bx.items[1] + nest[0][1] + ps[0].x;
        j = j + 1;
    }
    return t;
}`, 98},
		{"string-struct-tuple", `struct P { x: i32, y: i32 }
function name(j: i32): string { return { let s = "ab"; s = s + "cd"; s }; }
function pt(j: i32): P { return { let p = P { x: j, y: 2 }; p }; }
function main(): i32 {
    let t = 0;
    let j = 0;
    while (j < 3) {
        let s: string = { let a = name(j); a = a + "e"; a };
        let p = if (j > 0) { let q = pt(j); q } else { let r = P { x: 9, y: 9 }; r };
        let tp = { let u = (j, [j, 1]); u };
        let k = { let w = s; w };
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
function total(xs: dyn Shape[]): i32 { let t = 0; for x in xs { t = t + x.area(); } return t; }
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
		// side() keeps the Square off the static aggregates, so the census
		// has its box to count.
		{"issue-10529", `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
@noinline
function side(): i32 { return 3; }
function main(): i32 {
    let s: Square = Square { side: side() };
    let d: dyn Shape = { let q = s; q };
    return d.area();
}`, 9},
		{"var-init", dynShapesPrelude + `function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s = Square { side: j + 2 };
        let r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        let a: dyn Shape = { let q = s; q };
        let b: dyn Shape = if (j > 0) { let q = r; q } else { let z = Rect { w: 1, h: 1, tag: "z" }; z };
        let c: dyn Shape = match (j) { 0 => { let q = r; q }, _ => r };
        let n: dyn Shape = { let inner = { let q = r; q }; inner };
        let d: dyn Shape = s;
        d = { let q = r; q };
        t = t + a.area() + b.area() + c.area() + n.area() + d.area() + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 123},
		{"call-arg", dynShapesPrelude + `function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s = Square { side: j + 2 };
        let r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + measure({ let q = s; q });
        t = t + measure(if (j > 0) { let q = r; q } else { let z = Rect { w: 1, h: 1, tag: "z" }; z });
        t = t + measure(match (j) { 0 => { let q = r; q }, _ => r });
        t = t + measure({ let inner = { let q = r; q }; inner });
        t = t + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 102},
		{"return", dynShapesPrelude + `function blk(s: Square): dyn Shape { return { let q = s; q }; }
function cond(j: i32, r: Rect): dyn Shape { return if (j > 0) { let q = r; q } else { let z = Rect { w: 1, h: 1, tag: "z" }; z }; }
function pick(j: i32, r: Rect): dyn Shape { return match (j) { 0 => { let q = r; q }, _ => r }; }
function fresh(j: i32): dyn Shape { return { let q = Rect { w: j, h: 2, tag: "x" + "y" }; q }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s = Square { side: j + 2 };
        let r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        let a = blk(s);
        let b = cond(j, r);
        let c = pick(j, r);
        let f = fresh(j);
        t = t + a.area() + b.area() + c.area() + f.area() + s.side + r.w;
        j = j + 1;
    }
    return t;
}`, 93},
		{"array-element", dynShapesPrelude + `function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let s = Square { side: j + 2 };
        let r = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + total([
            { let q = s; q },
            if (j > 0) { let q = r; q } else { let z = Rect { w: 1, h: 1, tag: "z" }; z },
            match (j) { 0 => { let q = r; q }, _ => r },
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
function sum(xs: i32[]): i32 { let t: i32 = 0; for x in xs { t = t + x; } return t; }
function slen(s: string): i32 { return s.len(); }
function px(p: P): i32 { return p.x; }
function mk(j: i32): i32[] { return [j, j, j]; }
function via_param(a: i32[], b: i32[], c: boolean): i32 { return sum(if (c) { a } else { b }); }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let a = [j, 5];
        a = a.append(1);
        let p = P { x: j, y: 2 };
        let s = "abcdefghij" + "klmnop";
        t = t + sum(if (j > 1) { [j] } else { [1, j] });
        t = t + sum(if (j > 1) { mk(j) } else { a });
        t = t + sum(match (j) { 0 => a, _ => [j, j] });
        t = t + sum(if (j > 0) { if (j > 1) { a } else { mk(j) } } else { [4] });
        t = t + via_param(a, mk(j), j > 1);
        t = t + px(if (j > 1) { p } else { let q = P { x: 3, y: 3 }; q });
        t = t + slen(if (j > 1) { s } else { s + "x" });
        t = t + (if (j > 1) { a } else { [1, 2] }).len();
        t = t + (if (j > 1) { a } else { mk(j) })[0];
        j = j + 1;
    }
    return t;
}`
	checkBalancedOnEveryBackend(t, "conditional-consumers", src, 130)
	// A match over a fresh Option whose arm yields the payload binding: the
	// binding is out of exprType's scope when the analysis asks, so the
	// counted yield read as a borrow and neither the bound result nor the
	// argument temp was released (#10552).
	checkBalancedOnEveryBackend(t, "match-payload-yield", `function sum(xs: i32[]): i32 { let t: i32 = 0; for x in xs { t = t + x; } return t; }
function opt(j: i32): Option[i32[]] { if (j > 0) { return Some([j, 1]); } return None; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        let a = [j, 5];
        a = a.append(1);
        let v = match (opt(j)) { Some(xs) => xs, None => a };
        let o = opt(j);
        let w = match (o) { Some(xs) => xs, None => [9] };
        t = t + sum(v) + sum(w) + sum(match (opt(j)) { Some(xs) => xs, None => [2] });
        j = j + 1;
    }
    return t;
}`, 44)
	// An array-view arm of a conditional argument borrows its source no longer
	// than the call, as a bare view argument does, so the source keeps its
	// release (#10553).
	checkBalancedOnEveryBackend(t, "lent-view-arms", `function sumv(xs: [i32]): i32 { return xs[0] + xs.len(); }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        let a = [j, 5];
        a = a.append(1);
        t = t + sumv(if (j > 1) { a[0:2] } else { a[1:3] });
        t = t + sumv(match (j) { 0 => a[0:1], _ => a[1:2] });
        j = j + 1;
    }
    return t;
}`, 42)
	// An owned dyn receiver temp is released once its method call returns
	// (#10554).
	checkBalancedOnEveryBackend(t, "dyn-owned-receiver", dynShapesPrelude+`function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    let d1: dyn Shape = Rect { w: 5, h: 1, tag: "s" + "" };
    while (j < 3) {
        t = t + mkd(j).area();
        t = t + (if (j > 1) { d1 } else { mkd(j) }).area();
        t = t + (match (j) { 0 => mkd(1), _ => d1 }).area();
        t = t + d1.area();
        j = j + 1;
    }
    return t;
}`, 58)
	checkBalancedOnEveryBackend(t, "dyn-conditional-args", dynShapesPrelude+`function mkd(j: i32): dyn Shape { return Rect { w: j, h: 2, tag: "q" + "r" }; }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let d1: dyn Shape = Square { side: j + 1 };
        let d2: dyn Shape = Rect { w: j, h: 3, tag: "ab" + "cd" };
        t = t + measure(if (j > 0) { d1 } else { d2 });
        t = t + measure(match (j) { 0 => d2, 1 => mkd(j), _ => { let q = d1; q } });
        j = j + 1;
    }
    return t;
}`, 34)
}

// A field read straight off an if-, match- or block value has to resolve the
// struct from the value's type; the IR's owner lookup knew only named shapes
// and refused these with `field access on unresolved struct ""`.
func TestFieldReadOffConditionalValue(t *testing.T) {
	e2eharness.BoxedProbes(t)
	const src = `struct P { x: i32, y: i32 }
function main(): i32 {
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let p = P { x: j, y: 2 };
        t = t + (match (j) { 0 => p, _ => P { x: 7, y: 1 } }).x;
        t = t + (if (j > 1) { p } else { P { x: 4, y: 5 } }).y;
        t = t + ({ let q = P { x: j, y: 6 }; q }).y;
        j = j + 1;
    }
    return t;
}`
	checkBalancedOnEveryBackend(t, "field-read", src, 44)
}
