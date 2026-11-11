package e2ecompiler

import (
	"strings"
	"testing"
)

// A command takes one mode, and a sub-flag only beside the flag it modifies.
// Before, `-lint -fmt` ran lint and dropped -fmt, and `-w` alone was ignored,
// each with exit 0.
func TestSelfHostCLIFlagRules(t *testing.T) {
	const src = "function main(): i32 { return 0; }\n"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-lint", "-fmt"}, "fern: -lint and -fmt are separate modes: give one of them\n"},
		{[]string{"-check", "-interp"}, "fern: -check and -interp are separate modes: give one of them\n"},
		{[]string{"-w"}, "fern: -w takes effect only with -fmt\n"},
		{[]string{"-check", "-d"}, "fern: -d takes effect only with -fmt\n"},
		{[]string{"-chunk", "x"}, "fern: -chunk takes effect only with -tangle\n"},
		{[]string{"-doc-source", "x"}, "fern: -doc-source takes effect only with -doc\n"},
		{[]string{"-qemu", "q"}, "fern: -qemu takes effect only with -run\n"},
		{[]string{"-lint-set", "x=deny"}, "fern: -lint-set takes effect only with -lint\n"},
		{[]string{"-apk-label", "x"}, "fern: -apk-label takes effect only with -apk\n"},
		{[]string{"-jni-java", "out"}, "fern: -jni-java takes effect only with -shared\n"},
		{[]string{"-fmt", "-w", "-d"}, "fern: -w and -d do different things with the result: give one of them\n"},
		{[]string{"-fmt", "-d", "-w"}, "fern: -w and -d do different things with the result: give one of them\n"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			stdout, stderr, code := arrayReportCLI(t, src, nil, c.args...)
			if code != 2 || stdout != "" || stderr != c.want {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 and %q", code, stdout, stderr, c.want)
			}
		})
	}
	for _, args := range [][]string{{"-check"}, {"-fmt", "-w=false"}, {"-check=false", "-interp"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, stderr, code := arrayReportCLI(t, src, nil, args...); code != 0 {
				t.Errorf("exit %d, stderr %q; want 0", code, stderr)
			}
		})
	}
}

// A mode written with no dash is read as the entry file. The message names the
// flag.
func TestSelfHostCLIBareModeWord(t *testing.T) {
	const src = "function main(): i32 { return 0; }\n"
	_, stderr, code := arrayReportCLI(t, src, nil, "check")
	const want = "fern: cannot read entry file check: not found\nfern: check is not a file: the mode flag is -check\n"
	if code != 1 || stderr != want {
		t.Errorf("exit %d, stderr %q; want exit 1 and %q", code, stderr, want)
	}
}
