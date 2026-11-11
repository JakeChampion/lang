package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func simdCastConstructionProgram() string {
	var src strings.Builder
	src.WriteString(`
@noinline function cast32(a: f64, b: f64, c: f64, d: f64): f32x4 {
  return __f32x4_of(a as f32, b as f32, c as f32, d as f32);
}
@noinline function swapped32(a: f64, b: f64, c: f64, d: f64): f32x4 {
  return __f32x4_of(d as f32, b as f32, a as f32, c as f32);
}
@noinline function shared32(a: f64, b: f64, c: f64, d: f64, out: usize): f32x4 {
  let x = a as f32;
  let v = __f32x4_of(x, b as f32, c as f32, d as f32);
  unsafe { __store_i64(out, f64_bits(x as f64)); }
  return v;
}
@noinline function step(out: usize, tag: i32, value: f64): f64 {
  unsafe { __store_i32(out, __load_i32(out) * 10 + tag); }
  return value;
}
@noinline function effects32(a: f64, b: f64, c: f64, d: f64, out: usize): f32x4 {
  return __f32x4_of(step(out, 1, a) as f32, step(out, 2, b) as f32, step(out, 3, c) as f32, step(out, 4, d) as f32);
}
@noinline function mixed32(a: f32, b: f64, c: f64, d: f64): f32x4 {
  return __f32x4_of(a, b as f32, c as f32, d as f32);
}
@noinline function sum32(a: f64, b: f64, c: f64, d: f64): f32x4 {
  let sum = (a as f32) + (b as f32);
  return __f32x4_of(sum, c as f32, d as f32, a as f32);
}
function check(v: f32x4, a: f32, b: f32, c: f32, d: f32): boolean {
  return __f32x4_lane_bits(v, 0) == f32_bits(a) as u32
    && __f32x4_lane_bits(v, 1) == f32_bits(b) as u32
    && __f32x4_lane_bits(v, 2) == f32_bits(c) as u32
    && __f32x4_lane_bits(v, 3) == f32_bits(d) as u32;
}
function main(): i32 {
  let words: i64[] = [`)
	words := []uint64{
		0, 0x8000000000000000, 0x7ff0000000000000, 0xfff0000000000000,
		0x7ff0000000000001, 0xfff8123456789abc, 0x7ff8123456789abc, 0xfff0000000000001,
		0x36a0000000000000, 0xb6a0000000000000, 0x380fffffc0000000, 0x3810000000000000,
		0x47efffffe0000000, 0xc7efffffe0000000, 0x3ff0000010000000, 0x3ff0000030000000,
	}
	rng := rand.New(rand.NewSource(417))
	for range 512 {
		words = append(words, rng.Uint64())
	}
	for _, word := range words {
		fmt.Fprintf(&src, "%di64,", int64(word))
	}
	src.WriteString(`];
  let buffer = __alloc_u8(8);
  let out = 0 as usize;
  unsafe { out = buffer as usize + 8 as usize; }
  for row in 0..words.len() / 4 {
    let at = row * 4;
    let aa = f64_from_bits(words[at]);
    let bb = f64_from_bits(words[at + 1]);
    let cc = f64_from_bits(words[at + 2]);
    let dd = f64_from_bits(words[at + 3]);
    let a = aa as f32;
    let b = bb as f32;
    let c = cc as f32;
    let d = dd as f32;
    if (!check(cast32(aa, bb, cc, dd), a, b, c, d)) { return 1; }
    if (!check(swapped32(aa, bb, cc, dd), d, b, a, c)) { return 2; }
    if (!check(shared32(aa, bb, cc, dd, out), a, b, c, d)) { return 3; }
    unsafe {
      if (__load_i64(out) != f64_bits(a as f64)) { return 4; }
      __store_i32(out, 0);
    }
    if (!check(effects32(aa, bb, cc, dd, out), a, b, c, d)) { return 5; }
    unsafe { if (__load_i32(out) != 1234) { return 6; } }
    if (!check(mixed32(a, bb, cc, dd), a, b, c, d)) { return 7; }
  }
  if (!check(sum32(16777217.0, -16777216.0, 3.0, 4.0), 0.0f32, 3.0f32, 4.0f32, 16777216.0f32)) { return 8; }
  return 0;
}`)
	return src.String()
}

func TestSelfHostSIMDCastConstruction(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := simdCastConstructionProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("vector construction changes scalar float bits: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostSIMDCastConstructionShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := filepath.Join(t.TempDir(), "construct.fern")
	if err := os.WriteFile(source, []byte(simdCastConstructionProgram()), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, source, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"cast32", "swapped32"} {
				body := ""
				if target == "wasm32-wasi" {
					text := string(data)
					start := strings.Index(text, "  (func $"+name+" ")
					if start < 0 {
						t.Fatalf("missing %s", name)
					}
					body = text[start:]
					if end := strings.Index(body[1:], "\n  (func "); end >= 0 {
						body = body[:end+1]
					}
					if strings.Contains(body, "f64.promote_f32") || strings.Count(body, "f32.demote_f64")+2*strings.Count(body, "f32x4.demote_f64x2_zero") != 4 {
						t.Errorf("%s retains scalar round trips:\n%s", name, body)
					}
				} else {
					body = asmFuncBody(t, string(data), "__fn_"+name)
					if target == "x86-64-linux" {
						if strings.Contains(body, "cvtss2sd ") || strings.Count(body, "cvtsd2ss ")+2*strings.Count(body, "cvtpd2ps ") != 4 {
							t.Errorf("%s retains scalar round trips:\n%s", name, body)
						}
					} else if strings.Contains(body, "fcvt d") || strings.Count(body, "fcvt s")+2*strings.Count(body, "fcvtn") != 4 {
						t.Errorf("%s retains scalar round trips:\n%s", name, body)
					}
				}
			}
		})
	}
}
