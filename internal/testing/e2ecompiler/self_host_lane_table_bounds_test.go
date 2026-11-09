package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func laneTableProgram(main string) string {
	values := make([]string, 256)
	for i := range values {
		values[i] = fmt.Sprintf("%du8", i)
	}
	return "const TABLE: u8[] = [" + strings.Join(values, ", ") + "];\n" + `
@noinline function masked32(seed: u32): u32 {
  let v = __f32x4_load_bytes(TABLE, 16 * ((seed & 15u32) as i32));
  return __f32x4_lane_bits(v, 0);
}
@noinline function masked64(seed: u32): u64 {
  let v = __f64x2_load_bytes(TABLE, 16 * ((seed & 15u32) as i32));
  return __f64x2_lane_bits(v, 0);
}
@noinline function scalar(seed: u32): i32 {
  return TABLE[128 + ((seed & 31u32) as i32)] as i32;
}
@noinline function topbyte(seed: u32): i32 {
  return TABLE[(seed >> 24u32) as i32] as i32;
}
@noinline function checked(at: i32): u32 {
  return __f32x4_lane_bits(__f32x4_load_bytes(TABLE, at), 0);
}
@noinline function partial(seed: u32): u32 {
  let at = 16 * ((seed & 15u32) as i32) + 1;
  return __f32x4_lane_bits(__f32x4_load_bytes(TABLE, at), 0);
}
@noinline function wrapped(seed: u32): u32 {
  let at = ((seed & 1u32) as i32) * 2147483647 + 16;
  return __f32x4_lane_bits(__f32x4_load_bytes(TABLE, at), 0);
}
@noinline function shifted(seed: u32): u32 {
  let at = ((seed & 1u32) as i32) << 31;
  return __f32x4_lane_bits(__f32x4_load_bytes(TABLE, at), 0);
}
@noinline function narrowed(seed: u32): u32 {
  return __f32x4_lane_bits(__f32x4_load_bytes(TABLE, seed as i32), 0);
}
@noinline function unknown(bytes: u8[], seed: u32): u32 {
  return __f32x4_lane_bits(__f32x4_load_bytes(bytes, 16 * ((seed & 15u32) as i32)), 0);
}
` + main
}

const laneTableValidMain = `
function main(): i32 {
  for i in 0..256 {
    let seed = (i as u32 << 24u32) | ((255 - i) as u32);
    let at = 16 * ((seed & 15u32) as i32);
    let low = TABLE[at] as u32 | (TABLE[at + 1] as u32 << 8u32)
      | (TABLE[at + 2] as u32 << 16u32) | (TABLE[at + 3] as u32 << 24u32);
    let high = TABLE[at + 4] as u32 | (TABLE[at + 5] as u32 << 8u32)
      | (TABLE[at + 6] as u32 << 16u32) | (TABLE[at + 7] as u32 << 24u32);
    if (masked32(seed) != low || masked64(seed) != (low as u64 | (high as u64 << 32u64))) { return 1; }
    if (scalar(seed) != 128 + ((seed & 31u32) as i32) || topbyte(seed) != i) { return 2; }
    if (checked(at) != low || unknown(TABLE, seed) != low) { return 3; }
  }
  if (partial(0u32) != checked(1) || wrapped(0u32) != checked(16)) { return 4; }
  if (shifted(0u32) != checked(0) || narrowed(0u32) != checked(0)) { return 5; }
  return 0;
}
`

func TestSelfHostLaneTableBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, laneTableProgram(laneTableValidMain), target); code != 0 {
				t.Fatalf("valid table span failed: exit %d\n%s", code, stderr)
			}
			for _, call := range []string{
				"checked(-1)", "checked(241)", "checked(256)", "partial(15u32)",
				"wrapped(1u32)", "shifted(1u32)", "narrowed(4294967295u32)",
				"unknown([0u8, 1u8, 2u8, 3u8, 4u8, 5u8, 6u8, 7u8, 8u8, 9u8, 10u8, 11u8, 12u8, 13u8, 14u8], 0u32)",
			} {
				t.Run(call, func(t *testing.T) {
					source := laneTableProgram("function main(): i32 { return " + call + " as i32; }")
					stderr, code := cli.exitOf(t, source, target)
					if code == 0 || !strings.Contains(stderr, "out of range") {
						t.Fatalf("invalid span did not trap: exit %d\n%s", code, stderr)
					}
				})
			}
		})
	}
}

func TestSelfHostLaneTableBoundsShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "tables.fern")
	if err := os.WriteFile(src, []byte(laneTableProgram(laneTableValidMain)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			path := cli.emit(t, src, target)
			asm, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"masked32", "masked64", "scalar", "topbyte"} {
				body := asmFuncBody(t, string(asm), "__fn_"+name)
				if strings.Contains(body, "__fern_oob_abort") {
					t.Errorf("%s retains a proven bounds check:\n%s", name, body)
				}
			}
			for _, name := range []string{"checked", "partial", "wrapped", "shifted", "narrowed", "unknown"} {
				body := asmFuncBody(t, string(asm), "__fn_"+name)
				if !strings.Contains(body, "__fern_oob_abort") {
					t.Errorf("%s lost a necessary bounds check:\n%s", name, body)
				}
			}
		})
	}
}
