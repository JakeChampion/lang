package x86_64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/platforms"
)

// `__fern_isatty` is what `std/cli`'s colour gate consults before deciding
// to emit ANSI escapes, so a helper that answers wrongly is invisible until
// someone's redirected output is full of `ESC[31m`. TCGETS (0x5401) via
// ioctl(2) is the question; only a terminal answers it.

const isattySrc = `function main(): i32 {
    if (isatty(1)) { return 1; }
    return 0;
}`

// fernIsattyBody returns the emitted text of __fern_isatty, from its label to
// the next `.size` directive, so an unrelated syscall cannot satisfy an
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

func TestX86IsattyUsesTcgets(t *testing.T) {
	body := fernIsattyBody(t, compile(t, isattySrc))
	for _, want := range []string{"mov esi, 21505", "syscall"} {
		if !strings.Contains(body, want) {
			t.Errorf("x86-64 __fern_isatty is missing %q — without the TCGETS ioctl "+
				"the helper cannot tell a terminal from a redirect", want)
		}
	}
	if !strings.Contains(body, "sete al") {
		t.Error("x86-64 __fern_isatty does not turn the ioctl result into a 0/1 " +
			"boolean, so the colour gate reads a raw -errno as true")
	}
}

// Off the process entry shape there is no kernel to ask, so the helper must
// be the constant 0 rather than a syscall into nothing — the freestanding
// rule in docs/FREESTANDING-CORE.md, and the reason `isatty` is core rather
// than capability-gated.
func TestX86FreestandingIsattyIsConstantFalse(t *testing.T) {
	body := fernIsattyBody(t, compileOpts(t, isattySrc, Options{Entry: platforms.EntryExports}))
	if strings.Contains(body, "syscall") {
		t.Errorf("hostless __fern_isatty issues a syscall:\n%s", body)
	}
	if !strings.Contains(body, "xor eax, eax") {
		t.Errorf("hostless __fern_isatty does not answer 0 (not a terminal):\n%s", body)
	}
}
