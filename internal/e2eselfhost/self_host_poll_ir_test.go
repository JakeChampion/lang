package e2eselfhost

import (
	"testing"
)

// TestSelfHostPollIRX86_64 is the first slice of putting async on the
// self-hosted compiler's IR path (docs/ASYNC-SELFHOST-IR.md): the `poll`
// readiness builtin now lowers to a dedicated IR op (`op_poll`) that the
// self-host x86-64 IR backend emits as a call into `__fn___fern_poll` (poll(2)
// over the fd set), reading the SELF-HOST array layout (len at [ptr+0],
// element i at [ptr+(i+1)*8]). Because it's a real op — not a
// `call_direct` to an unknown `poll` symbol — a `poll`-using module is
// now IR-ELIGIBLE rather than bailing (the AST emitter it used to fall to
// can't emit `poll` at all).
//
// The case polls an EMPTY fd set, which returns -1 without a syscall —
// deterministic, and enough to pin that the typed path compiles it and it
// runs to the interp oracle's value. The syscall/marshalling
// body mirrors the native `__fern_poll` (separately tested; the self-host one
// is compiled Fern since #2649), adapted to
// the self-host array ABI. Real-fd polling on the self-host arrives with
// the timer/socket builtins (later slices).
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
