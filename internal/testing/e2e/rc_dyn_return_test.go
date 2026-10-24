package e2e

import "testing"

const dynReturnPrelude = `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
impl Label for i32 { function a(self: Self): i32 { return self; } }
impl Label for string { function a(self: Self): i32 { return self.len(); } }
function pass(l: dyn Label): dyn Label { return l; }
function viaview(l: dyn Label): dyn Label { let v: dyn Label = l; return v; }
function other(l: dyn Label): dyn Label { return Box { name: "zz" + "z" }; }
function show(l: dyn Label): i32 { return l.a(); }
`

func dynReturnSrc(n string) string {
	return dynReturnPrelude + `function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    let keep: Box = Box { name: "kept" };
    while (i < ` + n + `) {
        let local: Box = Box { name: "b" + "c" };
        let r: dyn Label = pass(local);
        let p: dyn Label = pass(Box { name: "fresh" + "x" });
        let v: dyn Label = viaview(local);
        let q: dyn Label = other(keep);
        let k: dyn Label = pass(i);
        t = t + r.a() + p.a() + v.a() + q.a() + k.a() + show("lit") + local.name.len();
        i = i + 1;
    }
    return t;
}`
}

func dynReturnBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        let local: Box = Box { name: "b" + "c" };
        let r: dyn Label = pass(local);
        let p: dyn Label = pass(Box { name: "fresh" + "x" });
        let q: dyn Label = other(keep);
        let k: dyn Label = pass(i);
        sum = sum + r.a() + p.a() + q.a() + k.a() + show(i) + show("lit");
        i = i + 1;
    }
`
	}
	return dynReturnPrelude + `function main(): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    let keep: Box = Box { name: "kept" };
    let base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    let first: i32 = (__heap_bump_bytes() as i32) - base;
    let mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    let second: i32 = (__heap_bump_bytes() as i32) - mid;
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
			checkSanitizedBalanced(t, c.src, c.want, runSanitizeX86_64)
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

func TestDynReturnedBorrowThroughABranch(t *testing.T) {
	const head = `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
@noinline function ids(s: string): string { return s; }
`
	const body = `
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let x: Box = Box { name: ids("ab") + "c" };
        let y: Box = Box { name: ids("d") + "e" };
        let p: dyn Label = pick(i % 2, x, y);
        t = t + p.a() + x.name.len() + y.name.len();
        i = i + 1;
    }
    return t;
}`
	cases := []struct{ name, pick string }{
		{"if_returned", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return if (c == 0) { l } else { m }; }`},
		{"if_bound", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { let r: dyn Label = if (c == 0) { l } else { m }; return r; }`},
		{"match_returned", `function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return match (c) { 0 => l, _ => m }; }`},
		{"variant_match_returned", `enum Pick { First, Second(i32) }
function tag(c: i32): Pick { if (c == 0) { return Pick.First; } return Pick.Second(c); }
function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return match (tag(c)) { First => l, Second(_) => m }; }`},
		{"variant_payload_yielded", `enum Holder { Has(dyn Label), Empty }
function mk(c: i32): Holder { if (c == 0) { return Holder.Has(Box { name: ids("ab") + "c" }); } return Holder.Empty; }
function pick(c: i32, l: dyn Label, m: dyn Label): dyn Label { return match (mk(c)) { Has(d) => d, Empty => m }; }`},
	}
	for _, c := range cases {
		src := head + c.pick + body
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) { checkSanitizedBalanced(t, src, 23, runSanitizeX86_64) })
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
