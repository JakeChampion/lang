package e2ecompiler

import (
	"fmt"
	"testing"
)

func TestSelfHostLaneBitmask(t *testing.T) {
	src := `function main(): i32 {
  let sign_nan: f64 = f64_from_bits(-2251799813685248);
  if (__f64x2_bitmask(__f64x2_of(-0.0, 1.0)) != 1) { return 1; }
  if (__f64x2_bitmask(__f64x2_of(0.0, sign_nan)) != 2) { return 2; }
  if (__f64x2_bitmask(__f64x2_ne(__f64x2_of(1.0, 2.0), __f64x2_splat(1.0))) != 2) { return 3; }
  if (__f32x4_bitmask(__f32x4_of(1.0 as f32, -1.0 as f32, -0.0 as f32, 0.0 as f32)) != 6) { return 4; }
  let mask: i32 = 0;
  while (mask < 16) {
    let a: f32 = 1.0 as f32; let b: f32 = a; let c: f32 = a; let d: f32 = a;
    if ((mask & 1) != 0) { a = -1.0 as f32; }
    if ((mask & 2) != 0) { b = -1.0 as f32; }
    if ((mask & 4) != 0) { c = -1.0 as f32; }
    if ((mask & 8) != 0) { d = -1.0 as f32; }
    if (__f32x4_bitmask(__f32x4_of(a, b, c, d)) != mask) { return 5; }
    mask = mask + 1;
  }
  return 0;
}`
	testAccelerationTargets(t, src)
}

func TestSelfHostFloatBranchFlags(t *testing.T) {
	src := `@noinline function relations(a: f64, b: f64): i32 {
  let bits: i32 = 0;
  if (a < b) { bits = bits | 1; }
  if (a <= b) { bits = bits | 2; }
  if (a == b) { bits = bits | 4; }
  if (a != b) { bits = bits | 8; }
  if (a >= b) { bits = bits | 16; }
  if (a > b) { bits = bits | 32; }
  if (!(a < b)) { bits = bits | 64; }
  if (!(a <= b)) { bits = bits | 128; }
  if (!(a == b)) { bits = bits | 256; }
  if (!(a != b)) { bits = bits | 512; }
  if (!(a >= b)) { bits = bits | 1024; }
  if (!(a > b)) { bits = bits | 2048; }
  return bits;
}
function main(): i32 {
  let nan: f64 = f64_from_bits(9221120237041090560);
  let inf: f64 = f64_from_bits(9218868437227405312);
  if (relations(1.0, 2.0) != 11 + 256 + 1024 + 2048) { return 1; }
  if (relations(2.0, 1.0) != 56 + 64 + 128 + 256) { return 2; }
  if (relations(-0.0, 0.0) != 22 + 64 + 512 + 2048) { return 3; }
  if (relations(inf, inf) != 22 + 64 + 512 + 2048) { return 4; }
  if (relations(-inf, inf) != 11 + 256 + 1024 + 2048) { return 5; }
  if (relations(nan, 1.0) != 8 + 64 + 128 + 256 + 1024 + 2048) { return 6; }
  if (relations(1.0, nan) != 8 + 64 + 128 + 256 + 1024 + 2048) { return 7; }
  if (relations(nan, nan) != 8 + 64 + 128 + 256 + 1024 + 2048) { return 8; }
  return 0;
}`
	testAccelerationTargets(t, src)
}

func TestSelfHostVectorDoubling(t *testing.T) {
	src := `@noinline function twice(x: f64): f64 { return 2.0 * x; }
@noinline function vector(x: f64x2): f64x2 { return __f64x2_splat(2.0) * x; }
function main(): i32 {
  let values: i64[] = [0, -9223372036854775807 - 1, 1, -9223372036854775807, 4602678819172646912, 9218868437227405311, 9218868437227405312];
  for bits in values {
    let x: f64 = f64_from_bits(bits);
    let want: i64 = f64_bits(x + x);
    if (f64_bits(twice(x)) != want) { return 1; }
    let v: f64x2 = vector(__f64x2_of(x, -x));
    if (f64_bits(__f64x2_lane(v, 0)) != want) { return 2; }
    if (f64_bits(__f64x2_lane(v, 1)) != f64_bits(-x + -x)) { return 3; }
  }
  return 0;
}`
	testAccelerationTargets(t, src)
}

func TestSelfHostFractalLibraries(t *testing.T) {
	src := `import "std/parse";
import "std/binary";
import "std/png";
import "std/result";
function main(): i32 {
  if (parse.int64("9223372036854775807").unwrap_or(0) != 9223372036854775807) { return 1; }
  if (parse.int64("-9223372036854775808").unwrap_or(0) != -9223372036854775807 - 1) { return 2; }
  if (parse.int64("+17").unwrap_or(0) != 17 || parse.int64("-0").unwrap_or(1) != 0) { return 3; }
  for text in ["", "-", "+", "9223372036854775808", "-9223372036854775809", "99999999999999999999", " 1", "1 ", "1x", "1.0"] {
    if (parse.int64(text).is_ok()) { return 4; }
  }
  match (parse.int64("123x")) { Err(InvalidDigit(3)) => {}, _ => { return 13; }, }
  match (parse.int64("-")) { Err(Empty) => {}, _ => { return 14; }, }
  match (parse.int64("9223372036854775808")) { Err(Overflow) => {}, _ => { return 15; }, }
  if (parse.IntError.Overflow.message() != "decimal integer is outside the signed 64-bit range") { return 16; }
  let bytes: u8[] = binary.append_u64_be([], 0xfedcba9876543210 as u64);
  if (binary.read_u64_be(bytes, 0).unwrap_or(0 as u64) != 0xfedcba9876543210 as u64) { return 5; }
  if (binary.read_u32_be(bytes, 1).unwrap_or(0 as u32) != 0xdcba9876 as u32) { return 6; }
  if (binary.read_u64_be(bytes, -1).is_ok() || binary.read_u64_be(bytes, 1).is_ok() || binary.read_u32_be(bytes, 5).is_ok()) { return 7; }
  if (png.encode_rgb8(0, 1, []).is_ok() || png.encode_rgb8(1, -1, []).is_ok() || png.encode_rgb8(2147483647, 1, []).is_ok()) { return 8; }
  if (png.encode_rgb8(1, 2147483647, []).is_ok() || png.encode_rgb8(1, 1, []).is_ok()) { return 9; }
  match (png.encode_rgb8(1, 1, [255 as u8, 0 as u8, 128 as u8])) {
    Ok(image) => {
      if (image.len() < 57 || image[0] != 137 as u8 || image[1] != 80 as u8) { return 10; }
      if (binary.read_u32_be(image, 16).unwrap_or(0 as u32) != 1 as u32 || binary.read_u32_be(image, 20).unwrap_or(0 as u32) != 1 as u32) { return 11; }
    },
    Err(_) => { return 12; },
  }
  return 0;
}`
	testAccelerationTargets(t, src)
}

func TestSelfHostStaticNumericTables(t *testing.T) {
	src := `const FLOATS: f32[] = [0.1f32, -0.0f32, 16777217.0f32, 1e-40f32, 0.1f32 + 0.0f32];
const DOUBLES: f64[] = [0.1 + 0.2, -0.0, 1.0 / 10.0, 16777216.0];
const INTEGERS: i64[] = [-9223372036854775807 - 1, 9223372036854775807];
const UNSIGNED: u64[] = [0u64, 18446744073709551615u64];
@noinline function local_casts(): f32[] { return [(1.0 / 10.0) as f32, (16777217.0f32 as f64) as f32]; }
@noinline fip function narrow(i: i32): f32 { return FLOATS[i]; }
@noinline fip function wide(i: i32): f64 { return DOUBLES[i]; }
function main(): i32 {
  if (shared_expression()[0] != 167772160.0) { return 11; }
  let before: i64 = __heap_alloc_count();
  let casts: f32[] = local_casts();
  if (casts[0] != 0.1f32 || casts[1] != 16777216.0f32) { return 10; }
  let i: i32 = 0;
  while (i < 128) {
    if (f32_bits(narrow(i % 5)) != f32_bits(FLOATS[i % 5])) { return 1; }
    if (f64_bits(wide(i % 4)) != f64_bits(DOUBLES[i % 4])) { return 2; }
    i = i + 1;
  }
  if (__heap_alloc_count() != before) { return 3; }
  if (f32_bits(narrow(0)) != f32_bits(0.1f32) || f32_bits(narrow(1)) != -2147483647 - 1) { return 4; }
  if (narrow(2) != 16777216.0f32 || f32_bits(narrow(3)) != f32_bits(1e-40f32) || narrow(4) != 0.1f32) { return 5; }
  if (f64_bits(wide(0)) != f64_bits(0.1 + 0.2) || f64_bits(wide(1)) != -9223372036854775807 - 1 || wide(3) != 16777216.0) { return 6; }
  let alias: f32[] = FLOATS;
  let changed: f32[] = alias.with(0, 12.0f32).append(13.0f32);
  if (changed[0] != 12.0f32 || changed[5] != 13.0f32 || alias[0] != 0.1f32 || FLOATS[0] != 0.1f32) { return 7; }
  let doubles: f64[] = DOUBLES.with(0, 12.0);
  if (doubles[0] != 12.0 || DOUBLES[0] != 0.1 + 0.2) { return 8; }
  if (INTEGERS[0] != -9223372036854775807 - 1 || INTEGERS[1] != 9223372036854775807 || UNSIGNED[1] != 18446744073709551615u64) { return 9; }
  return 0;
}`
	src += "\n@noinline function shared_expression(): f64[] {\nlet x0: f64 = 1.25;\n"
	for i := 1; i <= 27; i++ {
		src += fmt.Sprintf("let x%d: f64 = x%d + x%d;\n", i, i-1, i-1)
	}
	src += "return [x27];\n}\n"
	testAccelerationTargets(t, src)
}

func testAccelerationTargets(t *testing.T, src string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, src, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			summary := leakSummaryLine(stderr)
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
				t.Fatalf("unbalanced census: %q (%v)", summary, err)
			}
		})
	}
}
