package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ssaUniqueTest = regexp.MustCompile(`call __fern_rc_is_unique (v\d+)\n`)

// ins of bench/perceus/rbtree.fern matches a node above its uniqueness test.
// A unique node keeps its value where it is, so only the side that copies a
// shared node reads the value (ssatokenkeep.shared_loads). Each load of the
// value, the word at offset 32, is in a block that takes a count on the
// children of the node, which only the copy of a shared node does.
func TestSelfHostSSASharedArmLoadsTheKeptWord(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../bench/perceus/fern/run_rbtree.fern")
	if err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=rbtree__ins")
	msg, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	form := string(msg)
	if end := strings.Index(form, "\n}\n"); end >= 0 {
		form = form[:end+1]
	}
	tests := map[string]int{}
	node := ""
	for _, m := range ssaUniqueTest.FindAllStringSubmatch(form, -1) {
		tests[m[1]]++
		if tests[m[1]] > tests[node] {
			node = m[1]
		}
	}
	if tests[node] < 3 {
		t.Fatalf("ins has no node with three uniqueness tests:\n%s", form)
	}
	load := "= ld_at 32 " + node + "\n"
	loads := 0
	for _, block := range ssaFormBlock.Split(form, -1) {
		n := strings.Count(block, load)
		if n > 0 && !strings.Contains(block, "call __fern_rc_inc") {
			t.Fatalf("a block that does not copy the node loads its value:\n%s", block)
		}
		loads += n
	}
	if loads == 0 {
		t.Fatalf("ins has no load of the value of %s:\n%s", node, form)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, env := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"2000"}, env); code != 0 {
				t.Fatalf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}
