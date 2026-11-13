package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ssaFillArm = regexp.MustCompile(`^\s*v\d+ = st_at \d+ v\d+ v\d+\n\s*br b\d+\s*$`)

// A cons-tail loop with two hole positions carries the address of its hole
// (ssaholeaddr.addresses). grow of ssaTokenKeepProg fills the left or the
// right child of the node before. Its register form held eight blocks of one
// store each, two for each fill. It now has no such block, and two edges
// into the loop compute an address.
func TestSelfHostSSAHoleAddress(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(ssaTokenKeepProg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SSA_ALLOC=grow")
	msg, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	form := string(msg)
	if end := strings.Index(form, "\n}\n"); end >= 0 {
		form = form[:end+1]
	}
	arms := 0
	for _, block := range ssaFormBlock.Split(form, -1) {
		if ssaFillArm.MatchString(block) {
			arms++
		}
	}
	if adds := strings.Count(form, "= binary add "); arms != 0 || adds != 2 {
		t.Fatalf("grow has %d blocks of one store and %d address sums, want 0 and 2:\n%s", arms, adds, form)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		if stderr, code := cli.exitOf(t, ssaTokenKeepProg, target); code != 51 {
			t.Errorf("%s: exit %d, want 51\n%s", target, code, stderr)
		}
	}
}
