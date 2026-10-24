package e2e

import "testing"

func dynAliasSrc(n string) string {
	return `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
struct Holder { l: dyn Label }
function hold(l: dyn Label): Holder { return Holder { l: l }; }
function pair(l: dyn Label): dyn Label[] { return [l, l]; }
function alias(l: dyn Label): i32 { let v: dyn Label = l; let w: dyn Label = v; return w.a(); }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let local: Box = Box { name: "b" + "c" };
        let h: Holder = hold(local);
        let xs: dyn Label[] = pair(local);
        t = t + h.l.a() + xs[0].a() + xs[1].a() + alias(local) + local.name.len();
        i = i + 1;
    }
    return t;
}`
}

func dynAliasBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        let local: Box = Box { name: "b" + "c" };
        let h: Holder = hold(local);
        let xs: dyn Label[] = pair(local);
        let f: () => i32 = later(local);
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
function alias(l: dyn Label): i32 { let v: dyn Label = l; let w: dyn Label = v; return w.a(); }
function later(l: dyn Label): () => i32 { return () => l.a(); }
function main(): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    let base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    let first: i32 = (__heap_bump_bytes() as i32) - base;
    let mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    let second: i32 = (__heap_bump_bytes() as i32) - mid;
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

func TestDynCaptureStoredPastTheBorrow(t *testing.T) {
	src := `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
function later(l: dyn Label): () => i32 { return () => l.a(); }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 10) {
        let local: Box = Box { name: "b" + "c" };
        let f: () => i32 = later(local);
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
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 40 {
			t.Errorf("got %d, want 40", got)
		}
	})
}

func TestDynOnlyCaptureDroppedLocallyBounded(t *testing.T) {
	src := `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
function run(n: i32): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let local: Box = Box { name: "b" + "c" };
        let l: dyn Label = local;
        let f: () => i32 = () => l.a();
        sum = sum + f();
        i = i + 1;
    }
    return sum;
}
function main(): i32 {
    let base: i32 = (__heap_bump_bytes() as i32);
    let s1: i32 = run(500);
    let first: i32 = (__heap_bump_bytes() as i32) - base;
    let mid: i32 = (__heap_bump_bytes() as i32);
    let s2: i32 = run(2000);
    let second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    if (s1 != 1000 || s2 != 4000) { return 2; }
    return 0;
}`
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("verdict %d, want 0 (1: heap grew with the churn; 2: wrong answer)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("verdict %d, want 0 (1: heap grew with the churn; 2: wrong answer)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("verdict %d, want 0 (1: heap grew with the churn; 2: wrong answer)", got)
		}
	})
}

func TestDynCaptureBesideOtherCaptures(t *testing.T) {
	src := `trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
function later(l: dyn Label, k: i32, s: string, m: dyn Label): () => i32 {
    return () => l.a() * 1000 + k * 100 + s.len() * 10 + m.a();
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 6) {
        let x: Box = Box { name: "b" + "c" };
        let y: Box = Box { name: "d" + "ef" };
        let f: () => i32 = later(x, 4, "q" + "rs" + "t", y);
        t = t + f() - 2443;
        i = i + 1;
    }
    return t + 7;
}`
	t.Run("x86_64-sanitize", func(t *testing.T) { checkSanitizedBalanced(t, src, 7, runSanitizeX86_64) })
	t.Run("arm64-sanitize", func(t *testing.T) { checkSanitizedBalanced(t, src, 7, runSanitizeArm64) })
	t.Run("wasm", func(t *testing.T) {
		if got := runWasm(t, src); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
}

func TestDynAliasStoredPastTheBorrowBounded(t *testing.T) {
	src := dynAliasBumpSrc("500", "2000")
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
        let local: Box = Box { name: "b" + "c" };
        let f: Holder = Holder { l: Box { name: "x" + "y" } };
        let l: Holder = Holder { l: local };
        let p: Holder = Holder { l: i };
        let h: Holder = hold(local);
        let w: Two = Two { n: 0, l: local, s: "k" + "lm" };
        let v: Two = Two { ...w, n: 1 };
        let u: Two = bump(w);
        let Holder { l: d } = hold(local);
        let tp: (dyn Label, i32) = (d, 1);
        let (x, n) = tp;
        t = t + f.l.a() + l.l.a() + p.l.a() + h.l.a() + v.l.a() + u.l.a() + v.n + u.s.len() + d.a() + x.a() + n;
        i = i + 1;
    }
`
}

func dynFieldSrc(n string) string {
	return dynFieldPrelude + `function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
` + dynFieldBody(n) + `    return t;
}`
}

func dynFieldBumpSrc(n, wider string) string {
	return dynFieldPrelude + `function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    let base: i32 = (__heap_bump_bytes() as i32);
` + dynFieldBody(n) + `    let first: i32 = (__heap_bump_bytes() as i32) - base;
    let mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + dynFieldBody(wider) + `    let second: i32 = (__heap_bump_bytes() as i32) - mid;
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

func TestDynFieldReleasedByStructDropBounded(t *testing.T) {
	src := dynFieldBumpSrc("500", "2000")
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
