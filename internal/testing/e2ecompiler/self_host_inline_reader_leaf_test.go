package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inlineReaderLeafProgram = `function peek(xs: u8[], at: i32): i32 { return xs[at] as i32 + 1; }
function pair(bs: [u8], at: i32): i32 { return bs[at] as i32 * 256 + bs[at + 1] as i32; }
function le32(bs: [u8], at: i32): u32 {
  return bs[at] as u32 | bs[at + 1] as u32 << 8 | bs[at + 2] as u32 << 16 | bs[at + 3] as u32 << 24;
}
function le64(bs: u8[], at: i32): i64 {
  return bs[at] as i64 | bs[at + 1] as i64 << 8 | bs[at + 2] as i64 << 16 | bs[at + 3] as i64 << 24
    | bs[at + 4] as i64 << 32 | bs[at + 5] as i64 << 40 | bs[at + 6] as i64 << 48 | bs[at + 7] as i64 << 56;
}
@noinline function count(bs: [u8]): i32 { return bs.len(); }
function passes(bs: [u8]): i32 { return count(bs) + 1; }
function lends(bs: [u8]): [u8] { return bs; }
@noinline function exercise(): i32 {
  let xs: u8[] = [];
  let i: i32 = 0;
  while (i < 16) { xs = xs.append((i * 37 + 5) as u8); i = i + 1; }
  if (peek(xs, 3) != 117) { return 1; }
  if (pair(xs, 2) != 20340) { return 2; }
  if (le32(xs[4:12], 1) != 755557310u32) { return 3; }
  if (le64(xs, 8) != 3462114457792500269i64) { return 4; }
  if (passes(xs) != 17) { return 5; }
  if (lends(xs)[15] != 48u8) { return 6; }
  return 0;
}
function main(): i32 {
  let result = exercise();
  if (result != 0) { return result; }
  print("ok"); return 0;
}
`

// An element or word read of a parameter is a leaf, so a small reader is
// spliced into its caller. A view parameter that escapes into a call or the
// result keeps its function out of line.
func TestSelfHostInlineReaderLeaf(t *testing.T) {
	runSemanticProgram(t, "reader-leaves", inlineReaderLeafProgram,
		[]string{"peek", "pair", "le32", "le64", "count", "passes", "lends", "exercise"},
		semInlineWants("ok\n"), "count", "lends")

	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "reader_leaves.fern")
	if err := os.WriteFile(src, []byte(inlineReaderLeafProgram), 0o644); err != nil {
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
		for _, name := range []string{"peek", "pair", "le32", "le64"} {
			if strings.Contains(body, call+name) {
				t.Errorf("%s still calls the reader %s", target, name)
			}
		}
		if !strings.Contains(body, call+"lends") {
			t.Errorf("%s inlined lends, whose view is its result", target)
		}
	}
}

func TestSelfHostInlineReaderLeafBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `function pair(bs: [u8], at: i32): i32 { return bs[at] as i32 + bs[at + 1] as i32; }
function main(): i32 {
  let xs: u8[] = [1u8, 2u8, 3u8];
  return pair(xs[0:3], args().len() + 1);
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := cli.exitOf(t, source, target)
			if code != 134 {
				t.Fatalf("exit %d, want bounds abort 134:\n%s", code, out)
			}
		})
	}
}
