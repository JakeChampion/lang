package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each function makes a lane value from constants, or from one argument
// that is not a constant. main checks lanes of each value.
const laneConstantProg = `@noinline function mask(): u8x16 {
  return __u8x16_of(63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8, 63 as u8, 0 as u8, 0 as u8, 0 as u8);
}

@noinline function digits(): u8x16 {
  return __u8x16_of(48 as u8, 49 as u8, 50 as u8, 51 as u8, 52 as u8, 53 as u8, 54 as u8, 55 as u8, 56 as u8, 57 as u8, 97 as u8, 98 as u8, 99 as u8, 100 as u8, 101 as u8, 255 as u8);
}

@noinline function signed(): i8x16 {
  return __i8x16_of(-1, 2, -3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, -128);
}

@noinline function words(): u16x8 {
  return __u16x8_of(1, 2, 3, 4, 5, 6, 7, 65535);
}

@noinline function signed_words(): i16x8 {
  return __i16x8_of(-1, 2, 3, 4, 5, 6, -32768, 32767);
}

@noinline function quads(): u32x4 {
  return __u32x4_of(5 as u32, 5 as u32, 5 as u32, 4294967290 as u32);
}

@noinline function signed_quads(): i32x4 {
  return __i32x4_of(-1, 2, -2147483648, 2147483647);
}

@noinline function mixed(x: u8): u8x16 {
  return __u8x16_of(x, 1 as u8, 2 as u8, 3 as u8, 4 as u8, 5 as u8, 6 as u8, 7 as u8, 8 as u8, 9 as u8, 10 as u8, 11 as u8, 12 as u8, 13 as u8, 14 as u8, 15 as u8);
}

function main(): i32 {
  let m = mask();
  if (__u8x16_lane(m, 0) != 63 as u8 || __u8x16_lane(m, 1) != 0 as u8 || __u8x16_lane(m, 12) != 63 as u8 || __u8x16_lane(m, 15) != 0 as u8) {
    return 1;
  }
  let d = digits();
  if (__u8x16_lane(d, 0) != 48 as u8 || __u8x16_lane(d, 3) != 51 as u8 || __u8x16_lane(d, 4) != 52 as u8 || __u8x16_lane(d, 10) != 97 as u8 || __u8x16_lane(d, 15) != 255 as u8) {
    return 2;
  }
  let s = signed();
  if (__i8x16_lane(s, 0) != -1 || __i8x16_lane(s, 1) != 2 || __i8x16_lane(s, 2) != -3 || __i8x16_lane(s, 15) != -128) {
    return 3;
  }
  let w = words();
  if (__u16x8_lane(w, 0) != 1 || __u16x8_lane(w, 1) != 2 || __u16x8_lane(w, 6) != 7 || __u16x8_lane(w, 7) != 65535) {
    return 4;
  }
  let sw = signed_words();
  if (__i16x8_lane(sw, 0) != -1 || __i16x8_lane(sw, 5) != 6 || __i16x8_lane(sw, 6) != -32768 || __i16x8_lane(sw, 7) != 32767) {
    return 5;
  }
  let q = quads();
  if (__u32x4_lane(q, 0) != 5 as u32 || __u32x4_lane(q, 2) != 5 as u32 || __u32x4_lane(q, 3) != 4294967290 as u32) {
    return 6;
  }
  let sq = signed_quads();
  if (__i32x4_lane(sq, 0) != -1 || __i32x4_lane(sq, 1) != 2 || __i32x4_lane(sq, 2) != -2147483648 || __i32x4_lane(sq, 3) != 2147483647) {
    return 7;
  }
  let x = mixed(200 as u8);
  if (__u8x16_lane(x, 0) != 200 as u8 || __u8x16_lane(x, 1) != 1 as u8 || __u8x16_lane(x, 15) != 15 as u8) {
    return 8;
  }
  return 0;
}
`

// A lane value whose 16 bytes are constants is four words of 32 bits, so
// the compiler makes it in at most four steps. A value with one argument
// that is not a constant keeps a step for each lane.
func TestSelfHostConstantLaneValueIsFourWords(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneConstantProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	steps := func(name, insn string) int {
		return strings.Count(tailCallBody(t, text, name), insn)
	}
	// The mask has one word four times: one splat and no insert.
	if n := steps("mask", "pinsr"); n != 0 {
		t.Errorf("mask has %d inserts, want 0:\n%s", n, tailCallBody(t, text, "mask"))
	}
	// The digits have four different words: one splat and three inserts of 32 bits.
	if b, d := steps("digits", "pinsrb"), steps("digits", "pinsrd"); b != 0 || d != 3 {
		t.Errorf("digits has %d inserts of a byte and %d of a word of 32 bits, want 0 and 3:\n%s", b, d, tailCallBody(t, text, "digits"))
	}
	if n := steps("mixed", "pinsrb"); n != 15 {
		t.Errorf("mixed has %d inserts of a byte, want 15", n)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneConstantProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
