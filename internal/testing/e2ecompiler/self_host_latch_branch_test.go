package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The loop of fold has two latches: the `continue` and the end of the body.
// The exit of the loop is the next block for one of them only.
const latchBranchProg = `@noinline function fold(xs: i32[], limit: i32): i32 {
  let s = 0;
  for i in 0..xs.len() {
    let x = xs[i];
    if (x > limit) { s -= x; continue; }
    let j = 0;
    while (j < 4) { s = s * 31 + x + j; j += 1; }
    s ^= i;
  }
  return s;
}
function main(): i32 {
  if (fold([1, 2, 3, 50, 4], 10) != 519980369) { return 1; }
  if (fold([], 10) != 0) { return 2; }
  if (fold([99], 10) != 0 - 99) { return 3; }
  return 0;
}
`

// On arm64 a latch whose loop exit is not the next block branches to the
// body when the test holds, and jumps to the exit when it fails. The other
// order runs a branch and a jump on each trip. So no jump that follows a
// conditional branch goes back to a label above it.
func TestSelfHostLatchBranchesToTheBody(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(latchBranchProg), 0o644); err != nil {
		t.Fatal(err)
	}
	arm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(arm), "fold")
	seen := map[string]bool{}
	pairs := 0
	conditional := false
	for _, line := range strings.Split(body, "\n") {
		text := strings.TrimSpace(line)
		switch {
		case strings.HasSuffix(text, ":"):
			seen[strings.TrimSuffix(text, ":")] = true
			continue
		case strings.HasPrefix(text, "b."):
			conditional = true
			continue
		case strings.HasPrefix(text, "b ") && conditional:
			pairs++
			if target := strings.TrimPrefix(text, "b "); seen[target] {
				t.Errorf("arm64: fold jumps back to %s after a conditional branch:\n%s", target, body)
			}
		}
		conditional = false
	}
	if pairs == 0 {
		t.Fatalf("arm64: fold has no jump after a conditional branch, so the test does not see a latch with a far exit:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if stderr, code := cli.exitOf(t, latchBranchProg, target); code != 0 {
			t.Errorf("%s: exit %d, want 0\n%s", target, code, stderr)
		}
	}
}
