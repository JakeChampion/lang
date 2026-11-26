package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The arm that calls note is laid out between the load of v and its last
// use, but v is dead in that arm.
const laneDeadAtCallProg = `@noinline function note(at: i32): i32 {
  return at * 3;
}

@noinline function sum_until_zero(bs: u8[]): i32 {
  let total = 0;
  let i = 0;
  let found = 0;
  while (i + 16 <= bs.len()) {
    let v = __u8x16_load(bs, i);
    let zero = __u8x16_bitmask(__u8x16_eq(v, __u8x16_splat(0 as u8))) != 0;
    if (zero) {
      found = note(i);
      i = bs.len();
    } else {
      total += __u8x16_bitmask(__u8x16_lt(v, __u8x16_splat(20 as u8)));
      total += __u8x16_bitmask(__u8x16_eq(__u8x16_and(v, __u8x16_splat(1 as u8)), __u8x16_splat(1 as u8)));
      total += __u8x16_lane(__u8x16_shl(v, 2), 3) as i32;
      i += 16;
    }
  }
  return total + found;
}

function main(): i32 {
  let bs = __alloc_u8(64);
  for i in 0..64 {
    bs = bs.with(i, (i + 1) as u8);
  }
  let a = sum_until_zero(bs);
  bs = bs.with(40, 0 as u8);
  let b = sum_until_zero(bs);
  if (a != 153370 || b != 109424) {
    return 1;
  }
  return 0;
}
`

var laneSlotMove = regexp.MustCompile(`movupd (%xmm\d+, -?\d+\(%rbp\)|-?\d+\(%rbp\), %xmm\d+)`)

// A lane value that no call crosses keeps its register. x86-64 saves no
// float register across a call, so a value that a call crosses is in a
// frame slot.
func TestSelfHostLaneDeadAtCallKeepsItsRegister(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneDeadAtCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(asm), "sum_until_zero")
	if moves := laneSlotMove.FindAllString(body, -1); len(moves) != 0 {
		t.Errorf("sum_until_zero moves a lane value through its frame %d time(s), want none:\n%s", len(moves), body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneDeadAtCallProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
