package e2e

import "testing"

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
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let b: dyn Label = Box { name: "b" + "c" };
        total = total + show(Shape.Line(i));
        i = i + 1;
    }
    return total;
}`
}

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

func TestDynCoercedArgTempBounded(t *testing.T) {
	src := dynArgTempBumpSrc("500", "2000")
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
    let t: i32 = 0;
    let i: i32 = 0;
    let keep: Box = Box { name: "kept" };
    let s: string = "ab" + "c";
    while (i < ` + n + `) {
        let local: Box = Box { name: "b" + "c" };
        t = t + show(Shape.Dot) + show(local) + show(keep) + show(i) + show(s);
        t = t + local.name.len();
        i = i + 1;
    }
    return t + keep.name.len() + s.len();
}`
}

func dynArgNonFreshBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        let local: Box = Box { name: "b" + "c" };
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
