package e2eharness

import (
	"fmt"
	"strings"
	"testing"
)

func RunDriverStdinExits(runner []string, bin, src string) error {
	cmd := RunX86_64Bin(runner, bin)
	cmd.Stdin = strings.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		return fmt.Errorf("driver did not exit normally")
	}
	return nil
}

func runStdinVerdict(t *testing.T, runner []string, bin, src string) string {
	t.Helper()
	cmd := RunX86_64Bin(runner, bin)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("path probe driver failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func EligBits(t *testing.T, progs []string, weights []int) int {
	t.Helper()
	gcc, runner := X86_64Tooling(t)
	bin := CachedDriverBin(t, gcc, RepoPath("compiler"), "drivers/asm_pathprobe_run.fern")
	got := 0
	for i, p := range progs {
		if runStdinVerdict(t, runner, bin, p) == "ir" {
			got += weights[i]
		}
	}
	return got
}
