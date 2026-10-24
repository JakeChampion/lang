package e2e

import "testing"

func dynCoerceLocalSrc(n string) string {
	return `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let s: Square = Square { side: 3 };
        let d: dyn Shape = s;
        acc = acc + d.area() + s.side;
        i = i + 1;
    }
    return acc;
}`
}

const dynCoerceLocalNoLoopSrc = `trait Shape { function area(self: Self): i32; }
struct Square { side: i32 }
impl Shape for Square { function area(self: Self): i32 { return self.side * self.side; } }
function main(): i32 {
    let s: Square = Square { side: 3 };
    let d: dyn Shape = s;
    return d.area();
}`

func dynCoerceLocalBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        let s: Boxed = Boxed { tag: "a heap string owned by the concrete behind dyn" };
        let d: dyn Shape = s;
        sum = sum + d.area() + s.tag.len();
        i = i + 1;
    }
`
	}
	return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
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

func TestDynCoerceFromStructLocalBounded(t *testing.T) {
	src := dynCoerceLocalBumpSrc("500", "2000")
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
