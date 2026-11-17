package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The index of hi, lo and capped is below 16 in each lane. The index of
// shr3, shl4 and wide can be 16 or more, and such a lane must give 0.
const laneSwizzleClampProg = `@noinline function hi(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_shr(v, 4)); }
@noinline function lo(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_and(v, __u8x16_splat(15 as u8))); }
@noinline function capped(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_min(v, __u8x16_splat(9 as u8))); }
@noinline function shr3(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_shr(v, 3)); }
@noinline function shl4(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_shl(v, 4)); }
@noinline function wide(t: u8x16, v: u8x16): u8x16 { return __u8x16_swizzle(t, __u8x16_and(v, __u8x16_splat(31 as u8))); }
function main(): i32 {
  let t = __u8x16_of(100 as u8, 101 as u8, 102 as u8, 103 as u8, 104 as u8, 105 as u8, 106 as u8, 107 as u8, 108 as u8, 109 as u8, 110 as u8, 111 as u8, 112 as u8, 113 as u8, 114 as u8, 115 as u8);
  let v = __u8x16_of(0 as u8, 1 as u8, 15 as u8, 16 as u8, 17 as u8, 31 as u8, 32 as u8, 127 as u8, 128 as u8, 129 as u8, 143 as u8, 200 as u8, 240 as u8, 254 as u8, 255 as u8, 9 as u8);
  if (__u8x16_lane(hi(t, v), 3) != 101 as u8 || __u8x16_lane(hi(t, v), 7) != 107 as u8 || __u8x16_lane(hi(t, v), 14) != 115 as u8) { return 1; }
  if (__u8x16_lane(lo(t, v), 2) != 115 as u8 || __u8x16_lane(lo(t, v), 3) != 100 as u8 || __u8x16_lane(lo(t, v), 13) != 114 as u8) { return 2; }
  if (__u8x16_lane(capped(t, v), 1) != 101 as u8 || __u8x16_lane(capped(t, v), 14) != 109 as u8) { return 3; }
  if (__u8x16_lane(shr3(t, v), 7) != 115 as u8 || __u8x16_lane(shr3(t, v), 8) != 0 as u8 || __u8x16_lane(shr3(t, v), 14) != 0 as u8) { return 4; }
  if (__u8x16_lane(shl4(t, v), 0) != 100 as u8 || __u8x16_lane(shl4(t, v), 1) != 0 as u8 || __u8x16_lane(shl4(t, v), 12) != 100 as u8) { return 5; }
  if (__u8x16_lane(wide(t, v), 2) != 115 as u8 || __u8x16_lane(wide(t, v), 3) != 0 as u8 || __u8x16_lane(wide(t, v), 11) != 108 as u8) { return 6; }
  return 0;
}
`

// On x86-64 a swizzle adds 0x70 with saturation to its index, so that
// pshufb gives 0 for an index above 15. An index that is below 16 in each
// lane does not get the add. The constant of an add is a row of read-only
// data, not a value that the function builds.
func TestSelfHostLaneSwizzleClamp(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneSwizzleClampProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for name, clamp := range map[string]bool{"hi": false, "lo": false, "capped": false, "shr3": true, "shl4": true, "wide": true} {
		body := tailCallBody(t, string(asm), name)
		if !strings.Contains(body, "vpshufb") {
			t.Errorf("%s has no vpshufb:\n%s", name, body)
		}
		if got := strings.Contains(body, "vpaddusb .L"); got != clamp {
			t.Errorf("%s: clamp from read-only data is %v, want %v:\n%s", name, got, clamp, body)
		}
		if strings.Contains(body, "0x70707070") || strings.Contains(body, "vpbroadcast") {
			t.Errorf("%s builds a constant:\n%s", name, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneSwizzleClampProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
