package e2e

import "testing"

func TestStructUpdateParamSpreadReuse(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"carry", `struct T { a: i32, n: i32 }
function inc(p: T): T { p = T { ...p, n: p.n + 1 }; return p; }
function main(): i32 { let x: T = T { a: 5, n: 0 }; x = inc(x); x = inc(x); x = inc(x); return x.n * 10 + x.a; }`, 35},
		{"two-spreads", `struct T { a: i32, n: i32 }
function emit(p: T): T { p = T { ...p, a: p.a + 10 }; p = T { ...p, n: p.n + 1 }; return p; }
function main(): i32 { let x: T = T { a: 0, n: 0 }; x = emit(x); x = emit(x); return x.a + x.n; }`, 22},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Run("x86-64-linux", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86-64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if got := runWasm(t, c.src); got != c.want {
					t.Errorf("wasm main() = %d, want %d", got, c.want)
				}
			})
		})
	}
}
