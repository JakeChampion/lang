package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each function shifts the lanes of its argument by a constant, and var
// shifts by a count that the compiler does not know. main checks lanes of
// each result, with counts above the lane width also.
const laneShiftByConstantProg = `@noinline function shr2(v: u8x16): u8x16 { return __u8x16_shr(v, 2); }
@noinline function shl4(v: u8x16): u8x16 { return __u8x16_shl(v, 4); }
@noinline function sar3(v: i8x16): i8x16 { return __i8x16_shr(v, 3); }
@noinline function w5(v: u16x8): u16x8 { return __u16x8_shl(v, 5); }
@noinline function q7(v: i32x4): i32x4 { return __i32x4_shr(v, 7); }
@noinline function var(v: u8x16, n: i32): u8x16 { return __u8x16_shr(v, n); }
function main(): i32 {
  let v = __u8x16_of(255 as u8, 1 as u8, 128 as u8, 77 as u8, 255 as u8, 1 as u8, 128 as u8, 77 as u8, 255 as u8, 1 as u8, 128 as u8, 77 as u8, 255 as u8, 1 as u8, 128 as u8, 200 as u8);
  if (__u8x16_lane(shr2(v), 0) != 63 as u8 || __u8x16_lane(shr2(v), 2) != 32 as u8 || __u8x16_lane(shr2(v), 15) != 50 as u8) { return 1; }
  if (__u8x16_lane(shl4(v), 0) != 240 as u8 || __u8x16_lane(shl4(v), 1) != 16 as u8 || __u8x16_lane(shl4(v), 3) != 208 as u8) { return 2; }
  let s = __i8x16_of(-128, 127, -1, 8, -128, 127, -1, 8, -128, 127, -1, 8, -128, 127, -1, -9);
  if (__i8x16_lane(sar3(s), 0) != -16 || __i8x16_lane(sar3(s), 1) != 15 || __i8x16_lane(sar3(s), 2) != -1 || __i8x16_lane(sar3(s), 15) != -2) { return 3; }
  let w = __u16x8_of(1, 2, 3, 4, 5, 6, 7, 65535);
  if (__u16x8_lane(w5(w), 0) != 32 || __u16x8_lane(w5(w), 7) != 65504) { return 4; }
  let q = __i32x4_of(-1024, 1024, -1, 2147483647);
  if (__i32x4_lane(q7(q), 0) != -8 || __i32x4_lane(q7(q), 1) != 8 || __i32x4_lane(q7(q), 2) != -1 || __i32x4_lane(q7(q), 3) != 16777215) { return 5; }
  if (__u8x16_lane(var(v, 2), 0) != 63 as u8 || __u8x16_lane(var(v, 10), 0) != 63 as u8) { return 6; }
  if (__u8x16_lane(__u8x16_shr(v, 10), 0) != 63 as u8 || __u8x16_lane(__u8x16_shl(v, 8), 1) != 1 as u8) { return 7; }
  return 0;
}
`

// A lane shift by a constant has its count in the instruction, on x86-64
// and on arm64. On x86-64, lanes of 8 bits take the shift of 16 bits and
// one mask of constant bytes, which is a row of read-only data.
func TestSelfHostLaneShiftByConstant(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneShiftByConstantProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	for name, want := range map[string]string{
		"shr2": "vpsrlw $2, ",
		"shl4": "vpsllw $4, ",
		"w5":   "vpsllw $5, ",
		"q7":   "vpsrad $7, ",
	} {
		body := tailCallBody(t, text, name)
		if !strings.Contains(body, want) {
			t.Errorf("%s has no %q:\n%s", name, want, body)
		}
		if strings.Contains(body, "%xmm2, %xmm") && name != "shr2" && name != "shl4" {
			t.Errorf("%s takes its count from a register:\n%s", name, body)
		}
		if byteLanes := name == "shr2" || name == "shl4"; byteLanes != strings.Contains(body, "(%rip), %xmm0, ") || strings.Contains(body, "vpbroadcastb") {
			t.Errorf("%s: only a shift of byte lanes takes a mask, and the mask is a row of read-only data:\n%s", name, body)
		}
	}
	// The count of var is in a register, so its mask is a shift of ones.
	if body := tailCallBody(t, text, "var"); !strings.Contains(body, "vpcmpeqb") {
		t.Errorf("var has no mask from a shift of ones:\n%s", body)
	}
	// On arm64 each shift by a constant is one instruction.
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"shr2": "ushr v",
		"shl4": "shl v",
		"sar3": "sshr v",
		"w5":   "shl v",
		"q7":   "sshr v",
		"var":  "ushl v",
	} {
		body := tailCallBody(t, string(arm), name)
		if !strings.Contains(body, want) || name != "var" && strings.Contains(body, "dup ") {
			t.Errorf("arm64 %s has no %q, or it takes its count from a register:\n%s", name, want, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneShiftByConstantProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
