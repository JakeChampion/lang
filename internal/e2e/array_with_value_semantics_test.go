package e2e

import "testing"

// TestArrayWithValueSemantics guards #2832: `Array.with(i, v)` is the pure
// (functional) element-set — it must return a NEW array and leave the
// receiver unchanged when the receiver is still read after the call. The
// compiled backends must not reuse the receiver's buffer in place at
// rc==1 (an in-place reuse / FBIP optimisation that's only sound when the
// receiver is dead after the call), or `a` and the result alias and `a`
// was mutated — disagreeing with the interpreter's value semantics.
//
// emitArraySet now rc-incs a LIVE-after receiver before
// __fern_arr_cow_inplace, forcing the copy path; a move receiver (last
// use / reassign-to-self / a fresh temp) keeps the allocation-free rc==1
// reuse. Every backend (+ interp) must agree.
func TestArrayWithValueSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Receiver read after `.with` → value semantics (copy): a[1] stays
		// 20, c[1] is 99 → 20*100 + 99 = 2099, exit 2099 & 0xFF = 51.
		{"live-after", `function main(): i32 { var a: i32[] = [10, 20, 30]; var c: i32[] = a.with(1, 99); return a[1] * 100 + c[1]; }`, 51},
		// Canonical reassign-to-self idiom keeps the in-place reuse fast path.
		{"reassign-self", `function main(): i32 { var w: i32[] = [10, 20, 30]; w = w.with(1, 99); return w[1]; }`, 99},
		// Receiver dead after `.with` → reuse is sound, value correct.
		{"dead-after", `function main(): i32 { var a: i32[] = [10, 20, 30]; var c: i32[] = a.with(2, 7); return c[2]; }`, 7},
		// Two independent `.with` off one live receiver: both copies, base intact.
		{"two-copies", `function main(): i32 { var a: i32[] = [1, 2, 3]; var b: i32[] = a.with(0, 50); var c: i32[] = a.with(0, 60); return a[0] * 100 + b[0] + c[0]; }`, 210},
		// Live receiver used inside a later expression, string elements.
		{"live-after-strings", `function main(): i32 { var a: string[] = ["xx", "y"]; var c: string[] = a.with(1, "zzz"); return a[1].len() * 10 + c[1].len(); }`, 13},
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
