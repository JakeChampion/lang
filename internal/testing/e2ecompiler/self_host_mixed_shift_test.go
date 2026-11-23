package e2ecompiler

import "testing"

// A shift computes at the common type of its two operands, so the count masks
// to the width of that type and the result keeps the bits that the type of the
// left operand does not hold.
const mixedShiftProgram = `
@noinline function unsigned_left(x: u32, s: i32): i64 { return x << s; }
@noinline function unsigned_right(x: u32, s: i32): i64 { return x >> s; }
@noinline function byte_left(x: u8, s: i32): i32 { return x << s; }
@noinline function wide_count(x: i32, s: i64): i64 { return x >> s; }
@noinline function narrow_count(x: i64, s: u8): i64 { return x << s; }
function main(): i32 {
  if (unsigned_left(2147483648u32, 1) != 4294967296i64) { return 1; }
  if (unsigned_left(1u32, 32) != 4294967296i64) { return 2; }
  if (unsigned_left(1u32, 64) != 1i64) { return 3; }
  if (unsigned_right(4294967295u32, 33) != 0i64) { return 4; }
  if (unsigned_right(4294967295u32, 4) != 268435455i64) { return 5; }
  if (byte_left(200u8, 4) != 3200) { return 6; }
  if (byte_left(1u8, 32) != 1) { return 7; }
  if (wide_count(-64, 3i64) != -8i64) { return 8; }
  if (wide_count(-64, 33i64) != -1i64) { return 9; }
  if (narrow_count(1i64, 40u8) != 1099511627776i64) { return 10; }
  return 0;
}
`

func TestSelfHostMixedShiftComputesAtJoin(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, mixedShiftProgram, target); code != 0 {
				t.Fatalf("mixed shift: exit %d\n%s", code, stderr)
			}
		})
	}
}
