package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// The byte-buffer and string-builder helpers copy in bulk: through the
// size-classed __fern_memcpy on x86-64, and on arm64 through the word loop the
// memcpy op uses, whose last word overlaps the one before it. The program
// pushes every length from 0 to 40, which reaches every size class, through a
// builder that starts at one byte, so each reserve copies too. It appends past
// the string builder's first 64 KiB, then checks every byte it gets back.
const bufCopyProg = `function matches(out: string, at: i32, alpha: string, lo: i32, n: i32): boolean {
    var i: i32 = 0;
    while (i < n) {
        if (out[at + i] != alpha[lo + i]) { return false; }
        i = i + 1;
    }
    return true;
}
function main(): i32 {
    var alpha: string = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ";
    var h: usize = buf_new(1);
    var k: i32 = 0;
    while (k <= 40) {
        buf_push_range(h, alpha, k % 7, k % 7 + k);
        buf_push(h, slice_unchecked(alpha, k % 5, k % 5 + k) + "");
        k = k + 1;
    }
    var out: string = buf_take(h);
    var at: i32 = 0;
    k = 0;
    while (k <= 40) {
        if (!matches(out, at, alpha, k % 7, k)) { return 1; }
        at = at + k;
        if (!matches(out, at, alpha, k % 5, k)) { return 2; }
        at = at + k;
        k = k + 1;
    }
    if (at != out.len()) { return 3; }
    var r: i32 = 0;
    while (r < 5000) {
        strbuf_append(slice_unchecked(alpha, r % 11, r % 11 + r % 41) + "");
        r = r + 1;
    }
    var big: string = strbuf_take();
    at = 0;
    r = 0;
    while (r < 5000) {
        if (!matches(big, at, alpha, r % 11, r % 41)) { return 4; }
        at = at + r % 41;
        r = r + 1;
    }
    if (at != big.len()) { return 5; }
    return 42;
}
`

func TestSelfHostBufCopy(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "bufcopy.fern")
	if err := os.WriteFile(src, []byte(bufCopyProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bulk := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\n\s+call __fern_memcpy\n`),
		"arm64-linux":  regexp.MustCompile(`\n\s+ldr x9, \[x1\], #8\n`),
	}
	helpers := []string{"__fern_buf_reserve", "__fern_buf_push", "__fern_buf_push_range", "__fern_buf_take",
		"__fern_strbuf_grow", "__fern_strbuf_append", "__fern_strbuf_take"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range helpers {
			m := regexp.MustCompile(`(?s)\n` + name + `:\n(.*?)\n\s+ret\n`).FindStringSubmatch(string(asm))
			if m == nil {
				t.Fatalf("%s: %s not in the listing", target, name)
			}
			if !bulk[target].MatchString(m[0]) {
				t.Errorf("%s: %s does not copy in bulk:\n%s", target, name, m[1])
			}
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1-3: a byte-buffer copy, 4-5: a string-builder copy)", tg.target, got)
		}
	}
}
