package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ssaUniqueTest = regexp.MustCompile(`call __fern_rc_is_unique (v\d+)\n`)

var ssaFormBlockName = regexp.MustCompile(`\n  (b\d+):\n`)

// ins of bench/perceus/rbtree.fern matches a node above its uniqueness test.
// The colour, the key and the value of a node share one word. Each arm of ins
// tests the colour, so a unique node keeps that word where it is: the
// unique side of a test does not read the word. Only the side that copies a
// shared node stores the word in the copy.
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
	var loads []string
	for _, m := range regexp.MustCompile(`(v\d+) = ld_at `+node+`\n`).FindAllStringSubmatch(form, -1) {
		loads = append(loads, m[1])
	}
	if loads == nil {
		t.Fatalf("ins has no load of the shared word of %s:\n%s", node, form)
	}
	words := strings.Join(loads, "|")
	word := regexp.MustCompile(`\b(` + words + `)\b`)
	blocks := map[string]string{}
	heads := ssaFormBlockName.FindAllStringSubmatchIndex(form, -1)
	for i, h := range heads {
		end := len(form)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		blocks[form[h[2]:h[3]]] = form[h[1]:end]
	}
	unique := regexp.MustCompile(`(v\d+) = call __fern_rc_is_unique ` + node + `\n\s*brif (v\d+) (b\d+) `)
	arms := 0
	for _, m := range unique.FindAllStringSubmatch(form, -1) {
		if m[1] != m[2] {
			continue
		}
		arms++
		if word.MatchString(blocks[m[3]]) {
			t.Fatalf("the unique side %s of a test of %s reads its shared word (%s):\n%s", m[3], node, words, blocks[m[3]])
		}
	}
	if arms < 3 {
		t.Fatalf("ins has %d unique sides of a test of %s, want 3 or more:\n%s", arms, node, form)
	}
	store := regexp.MustCompile(`= st_at v\d+ (` + words + `)\n`)
	stored := false
	for _, block := range blocks {
		if store.MatchString(block) && strings.Contains(block, "call __fern_rc_inc") {
			stored = true
		}
	}
	if !stored {
		t.Fatalf("no block that copies %s stores its shared word (%s):\n%s", node, words, form)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, env := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
			if stderr, code := cli.exitOfFileArgs(t, src, target, nil, []string{"2000"}, env); code != 0 {
				t.Fatalf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}
