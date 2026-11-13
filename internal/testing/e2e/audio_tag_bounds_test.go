package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func checkAudioTagBoundsSuite(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"# pass 24\n", "# fail 0\n", "1..24\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in audio tag bounds suite output:\n%s", want, out)
		}
	}
}

func audioTagBoundsCompiled(t *testing.T, target string, runner []string) {
	t.Helper()
	entry := langSrcAbs(t, "tests/stdlib/audio_tags_test.fern")
	bin := e2eharness.CompileSelfHostFile(t, target, entry, nil)
	args := append(append([]string{}, runner...), bin)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("audio tag bounds suite: %v\n%s", err, out)
	}
	checkAudioTagBoundsSuite(t, string(out))
}

func TestAudioTagBoundsInterp(t *testing.T) {
	entry := langSrcAbs(t, "tests/stdlib/audio_tags_test.fern")
	out, code := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), entry)
	if code != 0 {
		t.Fatalf("audio tag bounds suite exit %d:\n%s", code, out)
	}
	checkAudioTagBoundsSuite(t, string(out))
}

func TestAudioTagBoundsX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	audioTagBoundsCompiled(t, e2eharness.TargetX86_64Linux, runner)
}

func TestAudioTagBoundsArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	audioTagBoundsCompiled(t, e2eharness.TargetArm64Linux, runner)
}

func TestAudioTagBoundsWasm(t *testing.T) {
	audioTagBoundsCompiled(t, e2eharness.TargetWasm32Wasi, []string{e2eharness.Wasmtime(t), "run"})
}
