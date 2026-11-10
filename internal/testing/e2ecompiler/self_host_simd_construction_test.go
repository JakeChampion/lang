package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func simdConstructionProgram() string {
	var src strings.Builder
	src.WriteString(`
@noinline function construct32(a: f32, b: f32, c: f32, d: f32): f32x4 {
  return __f32x4_of(a, b, c, d);
}
@noinline function swapped32(a: f32, b: f32, c: f32, d: f32): f32x4 {
  return __f32x4_of(d, b, a, c);
}
@noinline function stack32(a: f32, b: f32, c: f32, d: f32, e: f32, f: f32, g: f32, h: f32, i: f32, j: f32, k: f32, l: f32): f32x4 {
  return __f32x4_of(j, l, a, b);
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
  for row in 0..words.len() / 4 {
    let at = row * 4;
    let a = f64_from_bits(words[at]) as f32;
    let b = f64_from_bits(words[at + 1]) as f32;
    let c = f64_from_bits(words[at + 2]) as f32;
    let d = f64_from_bits(words[at + 3]) as f32;
    let direct = construct32(a, b, c, d);
    let swapped = swapped32(a, b, c, d);
    let stacked = stack32(a, b, c, d, a, b, c, d, a, b, c, d);
    if (!check(direct, a, b, c, d)) { return 1; }
    if (!check(swapped, d, b, a, c)) { return 2; }
    if (!check(stacked, b, d, a, b)) { return 3; }
  }
  return 0;
}`)
	return src.String()
}

func TestSelfHostSIMDConstruction(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := simdConstructionProgram()
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("vector construction changes scalar float bits: exit %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostSIMDConstructionShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := filepath.Join(t.TempDir(), "construct.fern")
	if err := os.WriteFile(source, []byte(simdConstructionProgram()), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, source, target))
			if err != nil {
				t.Fatal(err)
			}
			body := asmFuncBody(t, string(data), "__fn_construct32")
			op := "cvtpd2ps "
			if target == "arm64-linux" {
				op = "fcvtn"
			}
			if strings.Count(body, op) != 2 {
				t.Errorf("construction needs two paired conversions:\n%s", body)
			}
		})
	}
}
