package e2e

import "testing"

func TestArrayWithValueSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"live-after", `function main(): i32 { let a: i32[] = [10, 20, 30]; let c: i32[] = a.with(1, 99); return a[1] * 100 + c[1]; }`, 51},
		{"reassign-self", `function main(): i32 { let w: i32[] = [10, 20, 30]; w = w.with(1, 99); return w[1]; }`, 99},
		{"dead-after", `function main(): i32 { let a: i32[] = [10, 20, 30]; let c: i32[] = a.with(2, 7); return c[2]; }`, 7},
		{"two-copies", `function main(): i32 { let a: i32[] = [1, 2, 3]; let b: i32[] = a.with(0, 50); let c: i32[] = a.with(0, 60); return a[0] * 100 + b[0] + c[0]; }`, 210},
		{"live-after-strings", `function main(): i32 { let a: string[] = ["xx", "y"]; let c: string[] = a.with(1, "zzz"); return a[1].len() * 10 + c[1].len(); }`, 13},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if got := runInterpByte(t, c.src); got != c.want {
					t.Errorf("interp = %d, want %d", got, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, c.src); got != c.want {
					t.Errorf("x86_64 = %d, want %d", got, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, got := compileAndRunArm64(t, c.src); got != c.want {
					t.Errorf("arm64 = %d, want %d", got, c.want)
				}
			})
			t.Run("wasm32-wasi", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
					t.Errorf("wasm = %d, want %d", got, c.want)
				}
			})
		})
	}
}
