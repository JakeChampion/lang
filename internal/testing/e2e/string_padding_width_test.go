package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func checkStringPaddingWidthSuite(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"# pass 17\n", "# fail 0\n", "1..17\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in string width suite output:\n%s", want, out)
		}
	}
}

func stringPaddingWidthCompiled(t *testing.T, target string, runner []string) {
	t.Helper()
	entry := langSrcAbs(t, "tests/stdlib/string_pad_width_test.fern")
	bin := e2eharness.CompileSelfHostFile(t, target, entry, nil)
	args := append(append([]string{}, runner...), bin)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("string width suite: %v\n%s", err, out)
	}
	checkStringPaddingWidthSuite(t, string(out))
}

func TestStringPaddingWidthInterp(t *testing.T) {
	entry := langSrcAbs(t, "tests/stdlib/string_pad_width_test.fern")
	out, code := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), entry)
	if code != 0 {
		t.Fatalf("string width suite exit %d:\n%s", code, out)
	}
	checkStringPaddingWidthSuite(t, string(out))
}

func TestStringPaddingWidthX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	stringPaddingWidthCompiled(t, e2eharness.TargetX86_64Linux, runner)
}

func TestStringPaddingWidthArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	stringPaddingWidthCompiled(t, e2eharness.TargetArm64Linux, runner)
}

func TestStringPaddingWidthWasm(t *testing.T) {
	stringPaddingWidthCompiled(t, e2eharness.TargetWasm32Wasi, []string{e2eharness.Wasmtime(t), "run"})
}
