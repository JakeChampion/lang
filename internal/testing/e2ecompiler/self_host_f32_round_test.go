package e2ecompiler

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
)

const f32RoundFunctions = `
@noinline function add(a: f32, b: f32): f32 { return a + b; }
@noinline function sub(a: f32, b: f32): f32 { return a - b; }
@noinline function mul(a: f32, b: f32): f32 { return a * b; }
@noinline function div(a: f32, b: f32): f32 { return a / b; }
@noinline function mad(a: f32, b: f32, c: f32): f32 { return a * b + c; }
@noinline function tenth(x: f32): f32 { return x * 0.1f32; }
`

// f32RoundProgram checks each f32 operation against the IEEE result of Go.
// Each NaN result only has to be a NaN.
func f32RoundProgram() string {
	special := []uint32{0, 0x80000000, 1, 0x80000001, 0x007fffff, 0x00800000, 0x3f800000, 0xbf800000, 0x3f800001, 0x4b800000, 0x7f7fffff, 0xff7fffff, 0x7f800000, 0xff800000, 0x7fc00000}
	rng := rand.New(rand.NewSource(585))
	var as, bs, cs []uint32
	for _, a := range special {
		for _, b := range special {
			as, bs, cs = append(as, a), append(bs, b), append(cs, special[(len(as)*7)%len(special)])
		}
	}
	for range 400 {
		as, bs, cs = append(as, rng.Uint32()), append(bs, rng.Uint32()), append(cs, rng.Uint32())
	}
	for range 400 {
		n := func() uint32 { return math.Float32bits(float32(rng.NormFloat64() * 1000)) }
		as, bs, cs = append(as, n()), append(bs, n()), append(cs, n())
	}
	list := func(src *strings.Builder, name string, words []uint32) {
		fmt.Fprintf(src, "let %s: i32[] = [", name)
		for i, w := range words {
			if i != 0 {
				src.WriteString(",")
			}
			fmt.Fprintf(src, "%d", int32(w))
		}
		src.WriteString("];\n")
	}
	var want [6][]uint32
	for i := range as {
		a, b, c := math.Float32frombits(as[i]), math.Float32frombits(bs[i]), math.Float32frombits(cs[i])
		want[0] = append(want[0], math.Float32bits(a+b))
		want[1] = append(want[1], math.Float32bits(a-b))
		want[2] = append(want[2], math.Float32bits(a*b))
		want[3] = append(want[3], math.Float32bits(a/b))
		want[4] = append(want[4], math.Float32bits(float32(a*b)+c))
		want[5] = append(want[5], math.Float32bits(a*float32(0.1)))
	}
	var src strings.Builder
	src.WriteString(f32RoundFunctions)
	src.WriteString(`function same(got: f32, want: i32): boolean {
  let w = f32_from_bits(want);
  if (w != w) { return got != got; }
  return f32_bits(got) == want;
}
function main(): i32 {
`)
	list(&src, "a", as)
	list(&src, "b", bs)
	list(&src, "c", cs)
	for k, words := range want {
		list(&src, fmt.Sprintf("w%d", k), words)
	}
	src.WriteString(`for i in 0..a.len() {
  let x = f32_from_bits(a[i]);
  let y = f32_from_bits(b[i]);
  let z = f32_from_bits(c[i]);
  if (!same(add(x, y), w0[i])) { return 1; }
  if (!same(sub(x, y), w1[i])) { return 2; }
  if (!same(mul(x, y), w2[i])) { return 3; }
  if (!same(div(x, y), w3[i])) { return 4; }
  if (!same(mad(x, y, z), w4[i])) { return 5; }
  if (!same(tenth(x), w5[i])) { return 6; }
}
return 0;
}
`)
	return src.String()
}

func TestSelfHostF32RoundMatchesIEEE(t *testing.T) {
	cli := newStrictCLI(t)
	src := f32RoundProgram()
	t.Run("x86-64-linux", func(t *testing.T) {
		if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 0 {
			t.Fatalf("f32 operation %d differs from the Go IEEE reference\n%s", code, out)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		gcc, qemu := arm64Tooling(t)
		if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", src)); code != 0 {
			t.Fatalf("f32 operation %d differs from the Go IEEE reference\n%s", code, out)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", src)); code != 0 {
			t.Fatalf("f32 operation %d differs from the Go IEEE reference\n%s", code, out)
		}
	})
}

// Each f32 result is rounded in float registers, and the rounding of an f32
// constant happens at compile time.
func TestSelfHostF32RoundShape(t *testing.T) {
	cli := newStrictCLI(t)
	src := f32RoundFunctions + "function main(): i32 { return mad(1.5f32, 2.0f32, tenth(3.0f32)) as i32; }\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			text := cli.emit(t, target, src)
			down, up, integer := "cvtsd2ss", "cvtss2sd", "movd "
			if target == "arm64-linux" {
				down, up, integer = "fcvt s0, ", "fcvt d", "fmov w"
			}
			if target == "wasm32-wasi" {
				down, up, integer = "f32.demote_f64", "f64.promote_f32", "i32.reinterpret_f32"
			}
			for name, rounds := range map[string]int{"mad": 2, "tenth": 1} {
				var body string
				if target == "wasm32-wasi" {
					start := strings.Index(text, "\n  (func $"+name+" ")
					if start < 0 {
						t.Fatalf("no %s function in WebAssembly", name)
					}
					body = text[start+1:]
					if end := strings.Index(body[1:], "\n  (func "); end >= 0 {
						body = body[:end+1]
					}
				} else {
					body = asmFuncBody(t, text, "__fn_"+name)
				}
				if strings.Count(body, down) != rounds || strings.Count(body, up) != rounds || strings.Contains(body, integer) {
					t.Errorf("%s wants %d roundings with no integer register:\n%s", name, rounds, body)
				}
			}
		})
	}
}
