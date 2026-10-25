package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const uniqueLoopProgram = `@noinline function grow(own out: u8[], n: i32): u8[] {
  let i: i32 = 0;
  while (i < n) {
    out = out.append((i * 3) as u8);
    if (i % 5 == 0) { out = out.append(255u8); }
    i = i + 1;
  }
  return out;
}
@noinline function lent(out: u8[], n: i32): u8[] {
  let o: u8[] = out;
  let i: i32 = 0;
  while (i < n) { o = o.append(i as u8); i = i + 1; }
  return o;
}
@noinline function grow_and_keep(out: u8[], n: i32): i32 {
  let first: u8[] = out;
  let i: i32 = 0;
  while (i < n) { out = out.append(i as u8); i = i + 1; }
  return out.len() + first.len();
}
@noinline function exercise(): i32 {
  let base: u8[] = [1u8, 2u8, 3u8];
  let kept: u8[] = base;
  let grown: u8[] = grow(base, 10);
  if (kept.len() != 3 || kept[2] != 3u8) { return 1; }
  if (grown.len() != 15 || grown[3] != 0u8 || grown[4] != 255u8 || grown[14] != 27u8) { return 2; }
  let fresh: u8[] = grow([], 7);
  if (fresh.len() != 9 || fresh[8] != 18u8) { return 3; }
  let again: u8[] = grow(fresh, 0);
  if (again.len() != 9 || again[0] != 0u8) { return 4; }
  let shared: u8[] = lent(kept, 2);
  if (shared.len() != 5 || kept.len() != 3 || kept[0] != 1u8 || shared[4] != 1u8) { return 5; }
  if (grow_and_keep([9u8], 4) != 6) { return 6; }
  return 0;
}
function main(): i32 {
  let result = exercise();
  if (result != 0) { return result; }
  print("ok"); return 0;
}
`

// A loop that grows a byte array it owns checks the count once before the
// loop, not at each push, and a shared array is copied, not written. A loop
// over a borrowed array keeps the push's own check, which writes a sole
// borrowed array in place.
func TestSelfHostUniqueLoop(t *testing.T) {
	runSemanticProgram(t, "unique-loop", uniqueLoopProgram,
		[]string{"grow", "lent", "grow_and_keep", "exercise"}, semInlineWants("ok\n"))

	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "unique_loop.fern")
	if err := os.WriteFile(src, []byte(uniqueLoopProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x86.s")
	if out, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, out)
	}
	asm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	grow := condBranchBody(t, string(asm), "grow")
	if n := strings.Count(grow, "_uq"); n != 3 {
		t.Errorf("grow has %d count-test labels, want the 3 of the one check before its loop:\n%s", n, grow)
	}
	if !strings.Contains(grow, "call __fn___fern_bytes_owned") {
		t.Errorf("grow does not copy a shared array before its loop:\n%s", grow)
	}
	if lent := condBranchBody(t, string(asm), "lent"); strings.Contains(lent, "__fern_bytes_owned") {
		t.Errorf("lent copies the array it borrows before its loop:\n%s", lent)
	}
	kept := condBranchBody(t, string(asm), "grow_and_keep")
	if strings.Contains(kept, "__fern_bytes_owned") {
		t.Errorf("grow_and_keep reads its array after the loop, so it must not take a copy first:\n%s", kept)
	}
}
