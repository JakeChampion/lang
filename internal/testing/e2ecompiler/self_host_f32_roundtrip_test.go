package e2ecompiler

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const f32RoundtripFunctions = `
import "std/f64";
@noinline function narrow_bits(x: f64): i32 { return f32_bits(x as f32); }
@noinline function repeated_from(bits: i32): f32 { return f32_from_bits(f32_bits(f32_from_bits(bits))); }
`

func f32RoundtripProgram() string {
	inputs := []uint64{0, 1, 0x8000000000000000, 0x8000000000000001, 0x7fefffffffffffff, 0xffefffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff0000000000001, 0xfff8000000001234}
	words := []uint32{0, 1, 0x80000000, 0x80000001, 0x007fffff, 0x00800000, 0x7f7fffff, 0xff7fffff, 0x7f800000, 0xff800000, 0x7f800001, 0xffc01234}
	for exponent := uint32(0); exponent < 255; exponent++ {
		word := exponent << 23
		x := float64(math.Float32frombits(word))
		y := float64(math.Float32frombits(word + 1))
		mid := (x + y) / 2
		for _, v := range []float64{math.Nextafter(mid, math.Inf(-1)), mid, math.Nextafter(mid, math.Inf(1))} {
			bits := math.Float64bits(v)
			inputs = append(inputs, bits, bits^0x8000000000000000)
		}
		words = append(words, word, word+1, word+0x7fffff)
	}
	rng := rand.New(rand.NewSource(417))
	for range 512 {
		inputs = append(inputs, rng.Uint64())
		words = append(words, rng.Uint32())
	}
	var src strings.Builder
	src.WriteString(f32RoundtripFunctions)
	src.WriteString("function main(): i32 {\nlet inputs: i64[] = [")
	for i, bits := range inputs {
		if i != 0 {
			src.WriteString(",")
		}
		fmt.Fprintf(&src, "%di64", int64(bits))
	}
	src.WriteString("];\nlet expected: i32[] = [")
	for i, bits := range inputs {
		if i != 0 {
			src.WriteString(",")
		}
		fmt.Fprintf(&src, "%d", int32(math.Float32bits(float32(math.Float64frombits(bits)))))
	}
	src.WriteString(`];
for i in 0..inputs.len() {
  let x = f64_from_bits(inputs[i]);
  let actual = narrow_bits(x);
  if (x.is_nan()) {
    if (!(f32_from_bits(actual) as f64).is_nan()) { return 1; }
  } else if ((expected[i] & 2147483647) == 0) {
    if ((actual & 2147483647) != 0) { return 2; }
  } else if (actual != expected[i]) { return 3; }
}
let words: i32[] = [`)
	for i, word := range words {
		if i != 0 {
			src.WriteString(",")
		}
		fmt.Fprintf(&src, "%d", int32(word))
	}
	src.WriteString("];\nlet promoted: i64[] = [")
	for i, word := range words {
		if i != 0 {
			src.WriteString(",")
		}
		fmt.Fprintf(&src, "%di64", int64(math.Float64bits(float64(math.Float32frombits(word)))))
	}
	src.WriteString(`];
for i in 0..words.len() {
  let actual = repeated_from(words[i]) as f64;
  let expected = f64_from_bits(promoted[i]);
  if (expected.is_nan()) {
    if (!actual.is_nan()) { return 4; }
  } else if (expected == 0.0) {
    if (actual != 0.0) { return 5; }
  } else if (f64_bits(actual) != promoted[i]) { return 6; }
}
return 0;
}
`)
	return src.String()
}

func TestSelfHostF32Roundtrip(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := f32RoundtripProgram()
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("f32 conversion differs from the Go IEEE reference: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostF32RoundtripShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "roundtrip.fern")
	source := f32RoundtripFunctions + `function main(): i32 { return narrow_bits(1.25) - f32_bits(repeated_from(1067450368)); }`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			path := cli.emit(t, src, target)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			down, up := "cvtsd2ss", "cvtss2sd"
			if target == "arm64-linux" {
				down, up = "fcvt s0, d0", "fcvt d0, s0"
			}
			if target == "wasm32-wasi" {
				down, up = "f32.demote_f64", "f64.promote_f32"
			}
			for _, name := range []string{"narrow_bits", "repeated_from"} {
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
				wantDown, wantUp := 1, 0
				if name == "repeated_from" {
					wantDown, wantUp = 0, 1
				}
				if strings.Count(body, down) != wantDown || strings.Count(body, up) != wantUp {
					t.Errorf("%s has extra precision conversions:\n%s", name, body)
				}
			}
		})
	}
}
