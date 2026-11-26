package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// halves narrows two values with one constant limit for each. rows narrows
// the 16 bytes at each step of a loop with a constant and an argument that do
// not change in the loop. The outer loop of calling makes a call, and its
// inner loop adds a splat. The loop of once makes a call and reads each of
// its splats one time.
const laneHoistProg = `@noinline function halves(a: u16x8, b: u16x8): u8x16 {
  return __u16x8_narrow(a, b);
}

@noinline function rows(p: usize, n: i32, k: i32): void {
  unsafe {
    for i in 0..n {
      let at = p + (i * 16) as usize;
      let v = __u8x16_load_ptr(at);
      let lo = __u8x16_widen_lo(v) * __u16x8_splat(k);
      let hi = __u8x16_widen_hi(v) * __u16x8_splat(k);
      __u8x16_store(at, __u16x8_narrow(__u16x8_shr(lo, 4), __u16x8_shr(hi, 4)));
    }
  }
}

@noinline function tick(n: i32): i32 {
  return n + 1;
}

@noinline function calling(p: usize, n: i32, m: i32): i32 {
  let t = 0;
  unsafe {
    for r in 0..n {
      t = tick(t);
      for i in 0..m {
        let at = p + (i * 16) as usize;
        __u8x16_store(at, __u8x16_load_ptr(at) + __u8x16_splat(1 as u8));
      }
    }
  }
  return t;
}

@noinline function once(p: usize, n: i32): i32 {
  let t = 0;
  unsafe {
    for i in 0..n {
      let at = p + (i * 16) as usize;
      let v = __u8x16_load_ptr(at);
      if (__u8x16_bitmask(__u8x16_eq(v, __u8x16_splat(0 as u8))) != 0) {
        t = tick(t);
      }
      __u8x16_store(at, v + __u8x16_splat(1 as u8));
    }
  }
  return t;
}

function main(): i32 {
  let h = halves(__u16x8_splat(300), __u16x8_splat(7));
  if (__u8x16_lane(h, 0) != 255 as u8 || __u8x16_lane(h, 15) != 7 as u8) {
    return 1;
  }
  unsafe {
    let p = __alloc(64);
    for i in 0..64 {
      __store_u8(p + i as usize, i * 3);
    }
    rows(p, 4, 20);
    for i in 0..64 {
      let want = i * 3 * 20 >> 4;
      if (want > 255) {
        want = 255;
      }
      if (__load_u8(p + i as usize) != want) {
        return 2 + i;
      }
    }
    if (calling(p, 3, 4) != 3 || __load_u8(p) != 3 || __load_u8(p + 63 as usize) != 239) {
      return 70;
    }
    __store_u8(p, 0);
    if (once(p, 4) != 1 || __load_u8(p) != 1 || __load_u8(p + 63 as usize) != 240) {
      return 71;
    }
    __free(p, 64);
  }
  return 0;
}
`

var backBranch = regexp.MustCompile(`(?m)^\s+(?:j[a-z]+|b\.[a-z]+|b)\s+(\.Lssa_\w+)$`)

// loopBodies is the text of each loop of a function: from the label that a
// branch goes back to, to that branch.
func loopBodies(body string) []string {
	var out []string
	for _, m := range backBranch.FindAllStringSubmatchIndex(body, -1) {
		label := "\n" + body[m[2]:m[3]] + ":\n"
		if at := strings.Index(body[:m[0]], label); at >= 0 {
			out = append(out, body[at:m[1]])
		}
	}
	return out
}

// Two lane operations that are the same merge into one, and a lane value
// that does not change in a loop is made one time in front of it, also when
// it is made of constants only. A lane value read in an inner loop does not
// move out of an outer loop that makes a call, and one that the loop reads
// one time does.
func TestSelfHostLaneOperationsMergeAndHoist(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneHoistProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, splat string }{{"x86-64-linux", "pshuf"}, {"arm64-linux", "dup "}} {
		asm, err := os.ReadFile(cli.emit(t, src, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		if n := strings.Count(tailCallBody(t, text, "halves"), tc.splat); n != 1 {
			t.Errorf("%s: halves makes the limit %d times, want 1:\n%s", tc.target, n, tailCallBody(t, text, "halves"))
		}
		loops := loopBodies(tailCallBody(t, text, "rows"))
		if len(loops) == 0 {
			t.Fatalf("%s: no loop in rows:\n%s", tc.target, tailCallBody(t, text, "rows"))
		}
		for _, l := range loops {
			if strings.Contains(l, tc.splat) {
				t.Errorf("%s: a loop of rows makes a splat:\n%s", tc.target, l)
			}
		}
		// The splat moves in front of the inner loop. No register keeps it
		// across the call, so it stays in the outer loop: in a frame slot,
		// each step of the inner loop would load it again.
		nested := loopBodies(tailCallBody(t, text, "calling"))
		if len(nested) != 2 {
			t.Fatalf("%s: calling has %d loops, want 2:\n%s", tc.target, len(nested), tailCallBody(t, text, "calling"))
		}
		outer, inner := nested[0], nested[1]
		if len(inner) > len(outer) {
			outer, inner = inner, outer
		}
		if strings.Contains(inner, tc.splat) || !strings.Contains(outer, tc.splat) {
			t.Errorf("%s: calling makes its splat in the wrong loop:\n%s", tc.target, outer)
		}
	}
	// Out of a loop with a call, a lane value is in a frame slot, and the loop
	// loads it at each read. The loop of once reads each splat one time, so
	// they move. A u8 splat is pshufb by a zero register on x86-64 and a dup
	// of a general register on arm64. The arm64 bitmask makes its own dup.
	for _, tc := range []struct {
		target string
		splat  *regexp.Regexp
	}{{"x86-64-linux", regexp.MustCompile(`pshufb %xmm2, %xmm\d+`)}, {"arm64-linux", regexp.MustCompile(`dup v\d+\.16b, w\d+`)}} {
		asm, err := os.ReadFile(cli.emit(t, src, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		loops := loopBodies(tailCallBody(t, string(asm), "once"))
		if len(loops) == 0 {
			t.Fatalf("%s: no loop in once:\n%s", tc.target, tailCallBody(t, string(asm), "once"))
		}
		for _, l := range loops {
			if tc.splat.MatchString(l) {
				t.Errorf("%s: the loop of once makes a splat:\n%s", tc.target, l)
			}
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneHoistProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
