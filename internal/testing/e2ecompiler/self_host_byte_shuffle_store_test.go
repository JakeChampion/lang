package e2ecompiler

import "testing"

// ssa.combine_byte_stores makes one shuffled lane store of byte stores that
// read the lanes of a vector. The store carries the IR kind of a vstore, so
// each backend selects it.
const byteShuffleStoreProgram = `
@noinline
unsafe function narrow(destination: usize, words: f32x4): void {
  unsafe {
    let a = __f32x4_lane_bits(words, 0) as i32;
    let b = __f32x4_lane_bits(words, 1) as i32;
    let c = __f32x4_lane_bits(words, 2) as i32;
    let d = __f32x4_lane_bits(words, 3) as i32;
    __store_i32(destination, a & 65535 | b << 16);
    __store_i32(destination + 4 as usize, c & 65535 | d << 16);
  }
}

function main(): i32 {
  let src: u8[] = [];
  for i in 0..16 {
    src = src.append((i * 17) as u8);
  }
  let out = __alloc_u8(8);
  unsafe {
    narrow(out as usize + 8 as usize, __f32x4_load_bytes(src, 0));
  }
  let want = [0, 17, 68, 85, 136, 153, 204, 221];
  for i in 0..8 {
    if (out[i] as i32 != want[i]) {
      return 1 + i;
    }
  }
  return 0;
}
`

func TestSelfHostByteShuffleStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, byteShuffleStoreProgram, target); code != 0 {
				t.Fatalf("shuffled byte store: exit %d\n%s", code, stderr)
			}
		})
	}
}
