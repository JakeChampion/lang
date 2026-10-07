package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The VCL caching proxy (examples/vcl) built by the self-host compiler,
// through its CLI so the example's own modules load as a program's do.
func selfHostVCLBinary(t *testing.T, cli *selfHostCLI, src string) e2eharness.VCLStart {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("../../../examples/vcl", src))
	if err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, path)
	return func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
}

func TestSelfHostVCLProxyServesAndCaches(t *testing.T) {
	cli := buildSelfHostCLI(t)
	e2eharness.CheckVCLProxyServesAndCaches(t, selfHostVCLBinary(t, cli, "origin.fern"), selfHostVCLBinary(t, cli, "vclproxy.fern"))
}

func TestSelfHostVCLProxyRejectsABadPolicyAtLoadTime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	e2eharness.CheckVCLProxyRejectsABadPolicy(t, selfHostVCLBinary(t, cli, "vclproxy.fern"))
}

// The evaluator's TAP suite built under FERN_SANITIZE=1 (#11798). A carried
// donor dropped on a self tail call's back edge was dropped after the edge
// rebound the parameter it lived in, so the previous argument leaked and the
// next was over-released: a leak here, a use-after-free in the proxy. A leak
// finding leaves the exit code alone, so the output is checked too.
func TestSelfHostVCLBackendSuiteSanitized(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path, err := filepath.Abs("../../../examples/vcl/vclbackend_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, path, "FERN_SANITIZE=1")).CombinedOutput()
	if err != nil || strings.Contains(string(out), "fern-sanitizer:") || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("want a clean sanitized run with every case passing (%v):\n%s", err, out)
	}
}
