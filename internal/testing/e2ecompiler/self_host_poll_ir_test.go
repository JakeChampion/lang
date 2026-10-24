package e2ecompiler

import (
	"testing"
)

func TestSelfHostPollIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	main := `function main(): i32 {
    let fds: i32[] = [];
    return poll(fds, 0);
}`
	src := []byte(main + "\n")
	want := interpExit(t, interpBin, string(src))

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("poll([],0) exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
