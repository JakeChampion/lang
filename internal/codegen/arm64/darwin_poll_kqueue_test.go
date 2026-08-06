package arm64

import (
	"strings"
	"testing"
)

// `__fern_poll` is the readiness multiplexer std/async and std/tcp's deadline
// path are built on. On Linux it is ppoll(2); on arm64-darwin it was a bare
// `-1` stub for as long as the Darwin target has existed.
//
// A `-1` return means "nothing is ready", which is a LEGAL answer to poll —
// so the stub did not fail, it made every readiness wait give up instantly.
// `tcp_serve_deadline` fired its deadline at once and every std/async
// combinator (gather / race / with_deadline) returned as though it had timed
// out. Silent, and invisible to any test that only checks a program runs.
//
// This pins the kqueue implementation textually rather than by running it,
// because the runtime behaviour needs a macOS host and CI's macos-latest
// runner is the only place that exists. What a textual assertion CAN prove is
// the part most likely to regress by accident: that the Darwin path still
// issues the syscalls at all and has not been reverted to the stub.

// pollSrc forces `usesPoll`, pulling __fern_poll into the emitted runtime.
// The negative fd is deliberate — see TestArm64DarwinPollSkipsNegativeFds.
const pollSrc = `function main(): i32 {
    var fds: i32[] = [0 - 1, 1];
    return poll(fds, 10);
}`

// fernPollBody returns the emitted text of __fern_poll, from its label to the
// next `.size` directive. Scoping the assertions to the helper keeps an
// unrelated `svc` elsewhere in the runtime from satisfying them.
func fernPollBody(t *testing.T, asm string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, raw := range lines {
		if strings.TrimSpace(raw) == "__fern_poll:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("__fern_poll not emitted; the test cannot guard a helper that is absent")
	}
	for i := start; i < len(lines); i++ {
		if i > start && strings.Contains(lines[i], ".size") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

func TestArm64DarwinPollUsesKqueue(t *testing.T) {
	body := fernPollBody(t, compile(t, pollSrc, Options{Darwin: true}))

	// kqueue(2) = BSD 362, kevent(2) = BSD 363, close(2) = BSD 6.
	for _, want := range []string{"mov x16, #362", "mov x16, #363", "svc #0x80"} {
		if !strings.Contains(body, want) {
			t.Errorf("arm64-darwin __fern_poll is missing %q — the readiness path is not "+
				"reaching kqueue, so every std/async wait and tcp_serve_deadline will "+
				"report an instant timeout instead of blocking", want)
		}
	}

	// The stub was the whole body: `mov x0, #-1; ret`. -1 is still a legal
	// return (nothing ready), so its presence is fine; what must NOT be true
	// is that the helper returns without ever issuing a syscall.
	if !strings.Contains(body, "svc") {
		t.Error("arm64-darwin __fern_poll issues no syscall at all — this is the -1 stub, " +
			"which answers every readiness wait with 'nothing ready'")
	}
}

// The kqueue path must ignore negative fds the way poll(2) does. This is not
// hygiene: std/tcp's deadline path appends wasm_timer_pollable(...), which is
// -1 on native, directly into the fd set. kevent(2) does NOT ignore a
// negative fd — it fails that registration with EBADF — so without the skip
// the whole wait degrades.
func TestArm64DarwinPollSkipsNegativeFds(t *testing.T) {
	body := fernPollBody(t, compile(t, pollSrc, Options{Darwin: true}))
	if !strings.Contains(body, "b.lt .Lkq_skip") {
		t.Error("arm64-darwin __fern_poll has no negative-fd skip; std/tcp passes -1 " +
			"(wasm_timer_pollable on native) in the fd set and kevent(2) rejects it " +
			"with EBADF rather than ignoring it as poll(2) does")
	}
	// A failed registration comes back as an event with EV_ERROR (0x4000)
	// set, not as a kevent(2) failure. Treating it as readiness would report
	// a bad fd as ready.
	if !strings.Contains(body, "#16384") {
		t.Error("arm64-darwin __fern_poll does not mask EV_ERROR (0x4000) out of the " +
			"returned events, so a failed registration reads as readiness")
	}
}

// The Linux path must stay on ppoll — the Darwin branch is an addition, not a
// replacement, and a mix-up would be invisible until a Linux server stopped
// waiting.
func TestArm64LinuxPollStillPpoll(t *testing.T) {
	body := fernPollBody(t, compile(t, pollSrc, Options{}))
	if strings.Contains(body, "#362") || strings.Contains(body, "#363") {
		t.Error("arm64 Linux __fern_poll is issuing Darwin kqueue syscall numbers")
	}
	if !strings.Contains(body, "svc #0") {
		t.Error("arm64 Linux __fern_poll no longer issues its ppoll(2) syscall")
	}
}
