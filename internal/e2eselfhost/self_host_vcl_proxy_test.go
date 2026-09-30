package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The VCL caching proxy (examples/vcl) built by the self-host compiler,
// through its CLI so the example's own modules load as a program's do.
func selfHostVCLBinary(t *testing.T, cli *selfHostCLI, src string) e2eharness.VCLStart {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("../../examples/vcl", src))
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
