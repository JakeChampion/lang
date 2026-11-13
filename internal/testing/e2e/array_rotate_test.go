package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func checkArrayRotationSuite(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"# pass 17\n", "# fail 0\n", "1..17\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in rotation suite output:\n%s", want, out)
		}
	}
}

func arrayRotationCompiled(t *testing.T, target string, runner []string) {
	t.Helper()
	entry := langSrcAbs(t, "tests/stdlib/array_rotate_test.fern")
	bin := e2eharness.CompileSelfHostFile(t, target, entry, nil)
	args := append(append([]string{}, runner...), bin)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("rotation suite: %v\n%s", err, out)
	}
	checkArrayRotationSuite(t, string(out))
}

func TestArrayRotateCountInterp(t *testing.T) {
	entry := langSrcAbs(t, "tests/stdlib/array_rotate_test.fern")
	out, code := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), entry)
	if code != 0 {
		t.Fatalf("rotation suite exit %d:\n%s", code, out)
	}
	checkArrayRotationSuite(t, string(out))
}

func TestArrayRotateCountX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	arrayRotationCompiled(t, e2eharness.TargetX86_64Linux, runner)
}

func TestArrayRotateCountArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	arrayRotationCompiled(t, e2eharness.TargetArm64Linux, runner)
}

func TestArrayRotateCountWasm(t *testing.T) {
	arrayRotationCompiled(t, e2eharness.TargetWasm32Wasi, []string{e2eharness.Wasmtime(t), "run"})
}
