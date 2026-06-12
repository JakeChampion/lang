package e2e

import "testing"

// TestReturnInference checks that a function with no declared return type
// has it inferred from its `return` expressions, and the program runs
// correctly on every backend (the IR layer is target-agnostic, so
// arm64 / x86-64 / wasm and the interpreter must all agree).
func TestReturnInference(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"add-i32", `function add(a: i32, b: i32) { return a + b; } function main(): i32 { return add(40, 2); }`, 42},
		{"string-len", `function greet() { return "hi"; } function main(): i32 { return greet().len(); }`, 2},
		{"bool", `function pos(n: i32) { return n > 0; } function main(): i32 { if (pos(5)) { return 7; } return 0; }`, 7},
		{"branches", `function pick(b: boolean) { if (b) { return 10; } return 20; } function main(): i32 { return pick(true) + pick(false); }`, 30},
		{"struct", `struct P { x: i32, y: i32 } function mk() { return P { x: 3, y: 4 }; } function main(): i32 { var p = mk(); return p.x * 10 + p.y; }`, 34},
		{"chain", `function base() { return 21; } function dbl() { return base() * 2; } function main(): i32 { return dbl(); }`, 42},
		{"option-some", `function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(5)) { Some(v) => { return v; }, None => { return 0; } } return 9; }`, 5},
		{"option-none", `function find(n: i32) { if (n > 0) { return Some(n); } return None; } function main(): i32 { match (find(-1)) { Some(v) => { return v; }, None => { return 8; } } return 9; }`, 8},
		{"recurse-annotated-caller", `function twice(n: i32) { return n + n; } function main(): i32 { return twice(twice(10)) + 2; }`, 42},
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
			t.Run("wasm", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
		})
	}
}
