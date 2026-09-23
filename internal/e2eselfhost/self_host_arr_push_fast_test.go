package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An append that fits and may mutate its receiver runs in __fern_arr_push's
// frameless head; everything else (a full receiver, a shared one, and every
// append under the sanitizer, which checks the count first) takes the framed
// body. The program drives each path: appends to the immortal empty literal,
// in place and growing, and to an alias, which must copy and leave the
// original alone.
const arrPushFastProg = `function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 100) { a = a.append(i); i = i + 1; }
    var b: i32[] = a;
    b = b.append(1000);
    if (a.len() != 100) { return 1; }
    if (b.len() != 101) { return 2; }
    if (b[100] != 1000) { return 3; }
    var s: i32 = 0;
    for x in a { s = s + x; }
    if (s != 4950) { return 4; }
    return 42;
}
`

// arrPushHead is the listing from the helper's label to its first return.
func arrPushHead(t *testing.T, asm, label string) string {
	t.Helper()
	start := strings.Index(asm, label+":\n")
	if start < 0 {
		t.Fatalf("no %s in the listing", label)
	}
	rest := asm[start+len(label)+2:]
	end := regexp.MustCompile(`(?m)^\s+ret$`).FindStringIndex(rest)
	if end == nil {
		t.Fatalf("%s has no ret", label)
	}
	return rest[:end[1]]
}

func TestSelfHostArrPushFastPath(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "push.fern")
	if err := os.WriteFile(src, []byte(arrPushFastProg), 0o644); err != nil {
		t.Fatal(err)
	}
	frame := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\bpushq\b`),
		"arm64-linux":  regexp.MustCompile(`\bstp\b`),
	}
	store := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movq %rsi, 8\(%rdi,%rdx,8\)`),
		"arm64-linux":  regexp.MustCompile(`str x1, \[x0, x2, lsl #3\]`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, san := range []bool{false, true} {
			out := filepath.Join(dir, target+".s")
			cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
			if san {
				cmd.Env = append(os.Environ(), "FERN_RC_FREE_DEBUG=1")
			}
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
			}
			asm, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"__fern_arr_push", "__fern_arr_push_owned"} {
				head := arrPushHead(t, string(asm), label)
				if san {
					if !frame[target].MatchString(head) {
						t.Errorf("%s: under the sanitizer %s returns before building its frame:\n%s", target, label, head)
					}
					continue
				}
				if frame[target].MatchString(head) {
					t.Errorf("%s: %s builds a frame before its in-place append:\n%s", target, label, head)
				}
				if !store[target].MatchString(head) {
					t.Errorf("%s: %s's head does not append in place:\n%s", target, label, head)
				}
			}
		}
	}
	for _, tg := range h.targets {
		for _, san := range []bool{false, true} {
			bin := filepath.Join(dir, tg.target+".bin")
			cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
			if san {
				cmd.Env = append(os.Environ(), "FERN_RC_FREE_DEBUG=1")
			}
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != 42 {
				t.Errorf("%s (sanitizer %v): exit %d, want 42", tg.target, san, got)
			}
		}
	}
}
