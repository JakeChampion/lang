package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func buildVCLBinary(t *testing.T, bin, src, out string) e2eharness.VCLStart {
	t.Helper()
	exe := filepath.Join(t.TempDir(), out)
	cmd := exec.Command(bin, "-target", hostFernTarget(t), "-o", exe, src)
	cmd.Dir = langSrcAbs(t, "examples/vcl")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", src, err, o)
	}
	return func(args ...string) *exec.Cmd { return exec.Command(exe, args...) }
}

func TestVCLProxyServesAndCaches(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs two servers; skipped under -short")
	}
	bin := buildLangBinForInterp(t)
	e2eharness.CheckVCLProxyServesAndCaches(t, buildVCLBinary(t, bin, "origin.fern", "origin"), buildVCLBinary(t, bin, "vclproxy.fern", "vclproxy"))
}

func TestVCLProxyRejectsABadPolicyAtLoadTime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary; skipped under -short")
	}
	bin := buildLangBinForInterp(t)
	e2eharness.CheckVCLProxyRejectsABadPolicy(t, buildVCLBinary(t, bin, "vclproxy.fern", "vclproxy"))
}
