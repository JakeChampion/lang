package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// sum_lanes reads 16 bytes of a view of bytes as lanes. The view is an
// array, a part of an array, and the bytes of a string. With an argument the
// program reads 16 bytes that are not all inside the view.
const laneViewLoadProg = `import "std/string";
function sum_lanes(bs: [u8], at: i32): i32 {
  let v = __u8x16_load_view(bs, at);
  let total = 0;
  total += __u8x16_lane(v, 0) as i32;
  total += __u8x16_lane(v, 7) as i32;
  total += __u8x16_lane(v, 15) as i32;
  return total;
}

function main(): i32 {
  let n = 40;
  let bs = __alloc_u8(n);
  for i in 0..n {
    bs = bs.with(i, (i * 3 + 1) as u8);
  }
  // The whole array as a view, then a slice of it, then the bytes of a string.
  if (sum_lanes(bs, 0) != 1 + 22 + 46) {
    return 1;
  }
  if (sum_lanes(bs, 24) != 73 + 94 + 118) {
    return 2;
  }
  let part: [u8] = bs[5:30];
  if (sum_lanes(part, 2) != 22 + 43 + 67) {
    return 3;
  }
  if (sum_lanes(part, 9) != 43 + 64 + 88) {
    return 4;
  }
  let text = "0123456789abcdefghij";
  if (sum_lanes(text.as_bytes(), 4) != 52 + 98 + 106) {
    return 5;
  }
  let a = args();
  if (a.len() > 1) {
    // 25 bytes in the slice: index 10 needs bytes 10 to 25.
    if (a[1] == "past") {
      return sum_lanes(part, 10);
    }
    if (a[1] == "far") {
      return sum_lanes(part, 1000000);
    }
    if (a[1] == "negative") {
      return sum_lanes(part, -1);
    }
    if (a[1] == "array") {
      return sum_lanes(bs, 25);
    }
  }
  return 0;
}
`

// A lane load from a view of bytes gives the bytes of the view, and stops
// the program when the 16 bytes are not inside the view.
func TestSelfHostLaneLoadFromView(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(laneViewLoadProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOfFileArgs(t, src, target, nil, nil); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
		for _, outside := range []string{"past", "far", "negative", "array"} {
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{outside}); code != 134 {
				t.Errorf("%s %s: exit %d, want 134\n%s", target, outside, code, stderr)
			}
		}
	}
}
