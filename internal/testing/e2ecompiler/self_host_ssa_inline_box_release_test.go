package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `check` releases each `Nil` child, a static box. Nothing lives in a caller
// register at that release, so the emitters once called the helper there,
// and the helper made the tests of the count again. The edge after the free
// of a node once swapped its two children with one xchgq.
const inlineBoxReleaseProg = `enum Tree { Nil, Node(i64, Tree, Tree) }

function make(item: i64, depth: i32): Tree {
  if (depth == 0) {
    return Node(item, Nil, Nil);
  }
  return Node(item, make(2i64 * item - 1i64, depth - 1), make(2i64 * item, depth - 1));
}

function check(t: Tree): i64 {
  return match (t) { Node(item, l, r) => item + check(l) - check(r), Nil => 0i64 };
}

function main(): i32 {
  let total = 0i64;
  let i = 1i64;
  while (i <= 20i64) {
    total += check(make(i, 6)) + check(make(0i64 - i, 6));
    i += 1i64;
  }
  if (total != 0i64 - 40i64) {
    return 1;
  }
  return 0;
}
`

var releaseCall = regexp.MustCompile(`^\s+call __fn___sem_release_Tree\.r$`)

func TestSelfHostSSABoxReleaseIsInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "trees.fern")
	if err := os.WriteFile(src, []byte(inlineBoxReleaseProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "trees.s")
	if combined, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fn := functionListing(string(asm), "__fn_check")
	if fn == "" {
		t.Fatal("check not in the listing")
	}
	lines := strings.Split(fn, "\n")
	calls := 0
	for i, line := range lines {
		if !releaseCall.MatchString(line) {
			continue
		}
		calls++
		tested := false
		for j := i - 1; j >= 0 && j >= i-8; j-- {
			if strings.Contains(lines[j], "cmpl $0, -8(") {
				tested = true
				break
			}
		}
		if !tested {
			t.Errorf("a release of Tree calls the helper with no inline test of the count:\n%s", strings.Join(lines[max(0, i-8):i+1], "\n"))
		}
	}
	if calls == 0 {
		t.Fatalf("check calls no release of Tree, so the test checks nothing\n%s", fn)
	}
	if strings.Contains(fn, "xchgq") {
		t.Errorf("check swaps two registers with xchgq\n%s", fn)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 0 {
			t.Errorf("%s: exit %d, want 0", tg.target, code)
		}
	}
}
