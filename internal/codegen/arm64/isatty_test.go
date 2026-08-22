package arm64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/platforms"
)

// `__fern_isatty` is what `std/cli`'s colour gate consults before deciding
// to emit ANSI escapes, so a helper that answers wrongly is invisible until
// someone's redirected output is full of `ESC[31m`.
//
// Linux and Darwin ask the same question with different numbers: ioctl(2) is
// syscall 29 / BSD 54 and the terminal-attribute request is TCGETS (0x5401)
// / TIOCGETA (0x40487413). The Darwin half is pinned textually rather than by
// running it, for the reason darwin_poll_kqueue_test.go gives: macOS is only
// reachable on the `macos-15` CI runner.

const isattySrc = `function main(): i32 {
    if (isatty(1)) { return 1; }
    return 0;
}`

// fernIsattyBody returns the emitted text of __fern_isatty, from its label to
// the next `.size` directive, so an unrelated `svc` cannot satisfy an
// assertion meant for this helper.
func fernIsattyBody(t *testing.T, asm string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, raw := range lines {
		if strings.TrimSpace(raw) == "__fern_isatty:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("__fern_isatty not emitted; the test cannot guard a helper that is absent")
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], ".size") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

func TestArm64LinuxIsattyUsesTcgets(t *testing.T) {
	body := fernIsattyBody(t, compile(t, isattySrc, Options{}))
	for _, want := range []string{"mov x1, #21505", "mov x8, #29", "svc #0"} {
		if !strings.Contains(body, want) {
			t.Errorf("arm64 Linux __fern_isatty is missing %q — without the TCGETS "+
				"ioctl the helper cannot tell a terminal from a redirect", want)
		}
	}
	if !strings.Contains(body, "cset w0, eq") {
		t.Error("arm64 Linux __fern_isatty does not turn the ioctl result into a 0/1 " +
			"boolean, so the colour gate reads a raw -errno as true")
	}
}

func TestArm64DarwinIsattyUsesTiocgeta(t *testing.T) {
	body := fernIsattyBody(t, compile(t, isattySrc, Options{Darwin: true}))
	// Darwin's ioctl is BSD 54 via `svc #0x80`, and its terminal-attribute
	// request is TIOCGETA, too wide for a `mov` immediate.
	for _, want := range []string{"mov x16, #54", "svc #0x80", "=1078490131"} {
		if !strings.Contains(body, want) {
			t.Errorf("arm64-darwin __fern_isatty is missing %q — Darwin has its own "+
				"ioctl number and request constant, and Linux's would fail on it", want)
		}
	}
	if strings.Contains(body, "#21505") {
		t.Error("arm64-darwin __fern_isatty is issuing Linux's TCGETS request")
	}
}

// Off the process entry shape there is no kernel to ask, so the helper must
// be the constant 0 rather than a syscall into nothing — the freestanding
// rule in docs/FREESTANDING-CORE.md, and the reason `isatty` is core rather
// than capability-gated.
func TestArm64FreestandingIsattyIsConstantFalse(t *testing.T) {
	body := fernIsattyBody(t, compile(t, isattySrc, Options{Entry: platforms.EntryExports}))
	if strings.Contains(body, "svc") {
		t.Errorf("hostless __fern_isatty issues a syscall:\n%s", body)
	}
	if !strings.Contains(body, "mov x0, #0") {
		t.Errorf("hostless __fern_isatty does not answer 0 (not a terminal):\n%s", body)
	}
}
