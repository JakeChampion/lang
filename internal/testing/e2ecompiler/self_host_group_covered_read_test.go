package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const groupCoveredReadProgram = `function order(a: i32, b: i32): i32 {
  if (a < b) { return 0 - 1; }
  if (a > b) { return 1; }
  return 0;
}
function insertion(own a: i32[], lo: i32, hi: i32, desc: boolean): i32[] {
  let i = lo + 1;
  while (i < hi) {
    let v = a[i];
    let j = i - 1;
    let moving = j >= lo;
    while (moving) {
      let o = order(a[j], v);
      if (desc) {
        o = 0 - o;
      }
      if (o > 0) {
        a = a.with(j + 1, a[j]);
        j -= 1;
        moving = j >= lo;
      } else {
        moving = false;
      }
    }
    a = a.with(j + 1, v);
    i += 1;
  }
  return a;
}
@noinline function sorted(n: i32, desc: boolean): i32[] {
  let xs: i32[] = [];
  let seed = 7;
  for _ in 0..n {
    seed = (seed * 1103 + 12345) % 1000;
    xs = xs.append(seed);
  }
  if (n <= 8) {
    return insertion(xs, 0, n, desc);
  }
  return insertion(xs, 1, n, desc);
}
function main(): i32 {
  let up = sorted(8, false);
  let down = sorted(12, true);
  for i in 1..8 {
    if (up[i - 1] > up[i]) { return 1; }
  }
  for i in 2..12 {
    if (down[i - 1] < down[i]) { return 2; }
  }
  return 7;
}
`

// The inner loop of insertion reads a[j] two times and writes a[j + 1]. The
// first read leaves j in range, so the second read is not a member of a group
// with the write. A group test there adds instructions to the loop and puts
// insertion above the size limit of a leaf body.
func TestSelfHostGroupCoveredRead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run("run/"+target, func(t *testing.T) {
			out, code := cli.exitOf(t, groupCoveredReadProgram, target)
			if code != 7 {
				t.Fatalf("exit %d, want 7:\n%s", code, out)
			}
		})
	}

	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "group_covered_read.fern")
	if err := os.WriteFile(src, []byte(groupCoveredReadProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		path := filepath.Join(dir, target+".s")
		out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
		asm, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(asm), "\n__fn_insertion") {
			t.Errorf("%s keeps insertion out of line", target)
		}
		if !strings.Contains(string(asm), "\n__fn_sorted") {
			t.Errorf("%s has no listing of sorted", target)
		}
	}
}
