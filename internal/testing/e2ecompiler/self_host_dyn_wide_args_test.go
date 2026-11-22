package e2ecompiler

import "testing"

// The receiver and the ten arguments die at the call, so the arm64 allocator
// can put them in each caller-saved register that it has, x1 to x3 included.
const dynWideArgsSrc = `trait Wide { function mix(self: Self, a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32; }
struct P { k: i32 }
enum E { Lo(i32), Hi(i32) }
impl Wide for P { function mix(self: Self, a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 { return self.k + a + 2 * b + 3 * c + 4 * d + 5 * e + 6 * f + 7 * g + 8 * h + 9 * i + 10 * j; } }
impl Wide for i32 { function mix(self: Self, a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 { return self - a - 2 * b - 3 * c - 4 * d - 5 * e - 6 * f - 7 * g - 8 * h - 9 * i - 10 * j; } }
impl Wide for E { function mix(self: Self, a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 { let k = match (self) { Lo(n) => n, Hi(n) => n * 2 }; return k + a * b - c * d + e * f - g * h + i * j; } }
function call(w: dyn Wide, x: i32): i32 { return w.mix(x + 1, x + 2, x + 3, x + 4, x + 5, x + 6, x + 7, x + 8, x + 9, x + 10); }
function main(): i32 {
    let ws: dyn Wide[] = [P { k: 3 }, 100000, Lo(7), Hi(9)];
    let t = 0;
    for n in 0..12 {
        t = (t * 3 + call(ws[n % 4], t % 7 + n)) % 1000;
    }
    return t & 127;
}
`

func TestSelfHostDynWideArgs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	want := interpExit(t, buildLangBinForInterp(t), dynWideArgsSrc)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, dynWideArgsSrc, target); code != want {
				t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
			}
		})
	}
}
