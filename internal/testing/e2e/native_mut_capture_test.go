package e2e

import "testing"

func TestNativeMutScalarCapture(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"write-only", `function main(): i32 { let x = 1; let f = (): i32 => { x = 42; return 7; }; let r = f(); return r + x; }`, 49},
		{"counter", `function main(): i32 { let x = 0; let inc = (): i32 => { x = x + 1; return x; }; let a = inc(); let b = inc(); return x; }`, 2},
		{"counter-param", `function main(): i32 { let n = 10; let add = (d: i32): i32 => { n = n + d; return n; }; let a = add(5); let b = add(3); return n; }`, 18},
		{"returns-written", `function main(): i32 { let x = 5; let f = (): i32 => { x = x * 2; return x; }; return f(); }`, 10},
		{"read-only", `function main(): i32 { let x = 5; let f = (): i32 => { return x + 1; }; return f(); }`, 6},
		{"bool-capture", `function main(): i32 { let b = false; let t = (): i32 => { b = true; return 0; }; let r = t(); if (b) { return 1; } return 0; }`, 1},
		{"f64-capture", `function main(): i32 { let x: f64 = 1.0; let f = (): i32 => { x = 42.0; return 7; }; let r = f(); if (x > 41.0) { return 49; } return r; }`, 49},
		{"shared-cell", `function main(): i32 { let x = 0; let setter = (): i32 => { x = 4; return 0; }; let getter = (): i32 => { return x + 5; }; let a = setter(); return getter(); }`, 9},
		{"loop-counter", `function main(): i32 { let x = 0; let inc = (): i32 => { x = x + 1; return 0; }; let i = 0; while (i < 3) { let r = inc(); i = i + 1; } return x; }`, 3},
		{"outer-mutation", `function main(): i32 { let i = 0; let f = (): i32 => { return i; }; i = 5; return f(); }`, 5},
		{"loop-outer-mutation", `function main(): i32 { let s = 0; let i = 0; let add = (): i32 => { s = s + i; return 0; }; while (i < 4) { i = i + 1; add(); } return s; }`, 10},
		{"outer-and-inner", `function main(): i32 { let x = 0; let f = (): i32 => { x = x + 4; return 0; }; x = 3; f(); return x; }`, 7},
		{"fstring-boxed-read", `import "std/i32"; function main(): i32 { let x = 0; x = 74; let f = (): i32 => { return x; }; let s: string = f"{x}"; return s.len(); }`, 2},
		{"fstring-boxed-read-expr", `import "std/i32"; function main(): i32 { let x = 0; x = 70; let f = (): i32 => { return x; }; let s: string = f"{x + 4i32}"; return s.len(); }`, 2},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Run("x86_64", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, tc.src); got != tc.want {
					t.Errorf("x86-64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, got := compileAndRunArm64(t, tc.src); got != tc.want {
					t.Errorf("arm64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, tc.src); got != tc.want {
					t.Errorf("wasm %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
		})
	}
}

func TestNativeMutPointerCapture(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"array-reassign", `function main(): i32 { let a: i32[] = [10, 1]; let f: () => i32 = (): i32 => { return a[0]; }; a = [42, 1]; return f(); }`, 42},
		{"string-reassign", `function main(): i32 { let s: string = "aa"; let f: () => i32 = (): i32 => { return s.len(); }; s = "abcdef"; return f() + 36; }`, 42},
		{"struct-reassign", `struct B { v: i32 } function main(): i32 { let b: B = B { v: 10 }; let f: () => i32 = (): i32 => { return b.v; }; b = B { v: 42 }; return f(); }`, 42},
		{"struct-heap-field", `struct B { name: string, v: i32 } function main(): i32 { let b: B = B { name: "aa", v: 10 }; let f: () => i32 = (): i32 => { return b.v + b.name.len(); }; b = B { name: "abcd", v: 20 }; b = B { name: "abcdef", v: 30 }; return f() + 6; }`, 42},
		{"array-read-only", `function main(): i32 { let a: i32[] = [40, 2]; let f: () => i32 = (): i32 => { return a[0] + a[1]; }; return f(); }`, 42},
		{"alias-no-underflow", `function main(): i32 { let keep: i32[] = [40, 7]; let a: i32[] = [10, 1]; let f: () => i32 = (): i32 => { return a[0]; }; a = keep; a = [1, 2]; a = keep; let x: i32 = f() + keep[0]; if (x != 80) { return 100; } return __rc_underflow_count(); }`, 0},
		{"string-alias-no-underflow", `function main(): i32 { let keep: string = "abcdefgh"; let s: string = "aa"; let f: () => i32 = (): i32 => { return s.len(); }; s = keep; s = "abc"; s = keep; let x: i32 = f() + keep.len(); if (x != 16) { return 100; } return __rc_underflow_count(); }`, 0},
		{"loop-string-grow", `function main(): i32 { let s: string = "x"; let f: () => i32 = (): i32 => { return s.len(); }; let i: i32 = 0; while (i < 40) { s = s + "y"; i = i + 1; } return f() + 1; }`, 42},
		{"fstring-boxed-read", `import "std/string"; function main(): i32 { let s: string = "aa"; let f: () => i32 = (): i32 => { return s.len(); }; s = "bb"; let t: string = f"{s}"; return t.len(); }`, 2},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Run("x86_64", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, tc.src); got != tc.want {
					t.Errorf("x86-64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, got := compileAndRunArm64(t, tc.src); got != tc.want {
					t.Errorf("arm64 %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, tc.src); got != tc.want {
					t.Errorf("wasm %q: exit = %d, want %d", tc.name, got, tc.want)
				}
			})
		})
	}
}
