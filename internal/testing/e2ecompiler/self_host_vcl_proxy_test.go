package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
