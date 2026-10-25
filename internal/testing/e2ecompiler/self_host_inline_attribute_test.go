package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inlineAttributeProgram = `@inline
function common(bs: [u8], a: i32, b: i32, max: i32): i32 {
  let n: i32 = 0;
  while (n < max && bs[a + n] == bs[b + n]) { n = n + 1; }
  let h: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    h = (h ^ bs[a + i] as i32) * 31 + (h >> 3) - (bs[b + i] as i32 & 7) * 5 + (h & 255) * 3 - (h >> 7 ^ h << 2);
    h = h & 1023 | (h >> 11 & 3) << 10;
    i = i + 1;
  }
  return n * 4096 + h;
}
function plain(bs: [u8], a: i32, b: i32, max: i32): i32 {
  let n: i32 = 0;
  while (n < max && bs[a + n] == bs[b + n]) { n = n + 1; }
  let h: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    h = (h ^ bs[a + i] as i32) * 31 + (h >> 3) - (bs[b + i] as i32 & 7) * 5 + (h & 255) * 3 - (h >> 7 ^ h << 2);
    h = h & 1023 | (h >> 11 & 3) << 10;
    i = i + 1;
  }
  return n * 4096 + h;
}
@inline
function lends(bs: [u8]): [u8] { return bs; }
@noinline function exercise(xs: u8[]): i32 {
  if (common(xs, 0, 4, 4) != plain(xs, 0, 4, 4) || common(xs, 1, 5, 2) != plain(xs, 1, 5, 2)) { return 1; }
  if (common(xs, 0, 4, 4) >> 12 != 3 || plain(xs, 1, 5, 2) >> 12 != 2) { return 2; }
  if (lends(xs)[2] != 3u8) { return 3; }
  return 0;
}
function main(): i32 {
  let xs: u8[] = [1u8, 2u8, 3u8, 9u8, 1u8, 2u8, 3u8, 8u8];
  return exercise(xs);
}
`

// An @inline function is spliced at every call, even with loops, a body past
// the leaf limit and two callers. A view parameter that it only reads does not stop the splice, and
// one that it returns does.
func TestSelfHostInlineAttribute(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := cli.exitOf(t, inlineAttributeProgram, target)
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
		})
	}
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "inline_attribute.fern")
	if err := os.WriteFile(src, []byte(inlineAttributeProgram), 0o644); err != nil {
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
		body := condBranchBody(t, string(asm), "exercise")
		call := "call __fn_"
		if target == "arm64-linux" {
			call = "bl __fn_"
		}
		if strings.Contains(body, call+"common") {
			t.Errorf("%s still calls the @inline function common", target)
		}
		if !strings.Contains(body, call+"plain") {
			t.Errorf("%s spliced plain, which has two callers and no attribute", target)
		}
		if !strings.Contains(body, call+"lends") {
			t.Errorf("%s spliced lends, whose view is its result", target)
		}
	}
}
