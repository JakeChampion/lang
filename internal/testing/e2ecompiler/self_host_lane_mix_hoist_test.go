package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The step of mix_row in std/android/draw. The narrow holds each half to 255
// with a splat, two times in the step, and the loop has no call.
const laneMixHoistProg = `@noinline function mix(to: usize, from: usize, n: i32, w: i32): void {
  let opaque = __u16x8_of(0, 0, 0, 255, 0, 0, 0, 255);
  let k = __u16x8_splat(w);
  let keep = __u16x8_splat(256 - w);
  unsafe {
    for i in 0..n / 4 {
      let at = (i * 16) as usize;
      let s = __u8x16_load_ptr(from + at);
      let d = __u8x16_load_ptr(to + at);
      let lo = __u16x8_or(__u8x16_widen_lo(s), opaque) * k + __u8x16_widen_lo(d) * keep;
      let hi = __u16x8_or(__u8x16_widen_hi(s), opaque) * k + __u8x16_widen_hi(d) * keep;
      __u8x16_store(to + at, __u16x8_narrow(__u16x8_shr(lo, 8), __u16x8_shr(hi, 8)));
    }
  }
}

function main(): i32 {
  let a: usize = 0 as usize;
  let b: usize = 0 as usize;
  let t: i32 = 0;
  unsafe {
    a = __alloc(256);
    b = __alloc(256);
    for i in 0..64 {
      __store_i32(a + (i * 4) as usize, i * 16843009);
      __store_i32(b + (i * 4) as usize, 66051 + i * 7);
    }
  }
  for r in 0..3 {
    mix(a, b, 64, 100 + r * 50);
  }
  unsafe {
    for i in 0..64 {
      t = t * 31 + __load_i32(a + (i * 4) as usize);
    }
  }
  // The checksum from the compiler before lane values were merged and hoisted.
  if (t != -701197466) {
    return 1;
  }
  return 0;
}
`

// loopBody is the text from the label that the last backward branch of a
// function goes to, up to that branch.
func loopBody(t *testing.T, fn string, branch *regexp.Regexp) string {
	t.Helper()
	lines := strings.Split(fn, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := branch.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if lines[j] == m[1]+":" {
				return strings.Join(lines[j:i+1], "\n")
			}
		}
	}
	t.Fatalf("no backward branch in:\n%s", fn)
	return ""
}

func TestSelfHostLaneSplatLeavesTheLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneMixHoistProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target  string
		branch  *regexp.Regexp
		splat   *regexp.Regexp
		comment string
	}{
		// A u16 splat is movd, punpcklwd and pshufd. A widen made a zero register with vpxor.
		{"x86-64-linux", regexp.MustCompile(`^\s+j[a-z]+ (\.L\S+)$`), regexp.MustCompile(`(?m)^\s+(pshufd|punpcklwd|vpxor) `), "splat or zero register"},
		{"arm64-linux", regexp.MustCompile(`^\s+b\.[a-z]+ (\.L\S+)$`), regexp.MustCompile(`(?m)^\s+dup v`), "splat"},
	} {
		asm, err := os.ReadFile(cli.emit(t, src, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_mix")
		if fn == "" {
			t.Fatalf("%s: no __fn_mix in the listing", tc.target)
		}
		body := loopBody(t, fn, tc.branch)
		if got := tc.splat.FindAllString(body, -1); len(got) != 0 {
			t.Errorf("%s: the loop of mix builds a %s on each pass %v:\n%s", tc.target, tc.comment, got, body)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, laneMixHoistProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0 (the checksum of the buffer)\n%s", target, code, stderr)
		}
	}
}
