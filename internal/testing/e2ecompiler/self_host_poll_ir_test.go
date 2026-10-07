package e2ecompiler

import (
	"testing"
)

// TestSelfHostPollIRX86_64 pins the `poll` readiness builtin on the self-hosted
// compiler's x86-64 IR path (docs/ASYNC-SELFHOST-IR.md): it lowers to a
// dedicated IR op (`op_poll`) that the backend emits as a call into
// `__fn___fern_poll` (poll(2) over the fd set), reading the self-host array
// layout (len at [ptr+0], element i at [ptr+(i+1)*8]).
//
// The case polls an EMPTY fd set, which returns -1 without a syscall —
// deterministic, and enough to pin that the typed path compiles it and it
// runs to the interp oracle's value.
func TestSelfHostPollIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)

	// poll([], 0) → -1 (no fd ready); -1 truncates to exit code 255.
	main := `function main(): i32 {
    let fds: i32[] = [];
    return poll(fds, 0);
}`
	src := []byte(main + "\n")
	want := interpExit(t, interpBin, string(src)) // interp builtinPoll → -1 → 255

	if stderr, code := cli.exitOf(t, string(src), "x86-64-linux"); code != want {
		t.Errorf("poll([],0) exited %d, want %d (interp oracle)\n%s", code, want, stderr)
	}
}
