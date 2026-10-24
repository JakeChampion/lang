package e2e

import "testing"

func TestDeclaredReturnTypes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"add-i32", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(40, 2); }`, 42},
		{"string-len", `function greet(): string { return "hi"; } function main(): i32 { return greet().len(); }`, 2},
		{"bool", `function pos(n: i32): boolean { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`, 7},
		{"branches", `function pick(b: boolean): i32 { if (b) { return 10; } return 20; } function main(): i32 { return pick(true) + pick(false); }`, 30},
		{"struct", `struct P { x: i32, y: i32 } function mk(): P { return P { x: 3, y: 4 }; } function main(): i32 { let p = mk(); return p.x * 10 + p.y; }`, 34},
		{"chain", `function base(): i32 { return 21; } function dbl(): i32 { return base() * 2; } function main(): i32 { return dbl(); }`, 42},
		{"option-some", `function find(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(5)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 5},
		{"option-none", `function find(n: i32): Option[i32] { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(-1)) { Some(v) => { return v; }, None => { return 8; } } return 9; }`, 8},
		{"nested-call", `function twice(n: i32): i32 { return n + n; } function main(): i32 { return twice(twice(10)) + 2; }`, 42},
		{"void-fn", `function bump(n: i32): void { let x: i32 = n + 1; } function main(): i32 { bump(1); return 42; }`, 42},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp exit = %d, want %d", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
		})
	}
}
