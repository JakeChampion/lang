package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// examples/vcl/ ends in a real HTTP caching reverse proxy: a listening
// socket, a request parsed off the wire, VCL deciding what happens, a real
// TCP fetch from the declared backend on a miss, an in-memory cache that
// survives between requests, and a real response written back.
//
// This gate runs it. It builds the proxy and a counting origin, puts one
// in front of the other, and drives them with a real HTTP client.
//
// The origin is what makes caching PROVABLE. Every response carries the
// number of requests that process has actually served, so a second request
// answered with the SAME counter means the origin was never asked. Reading
// the proxy's own headers could never establish that — only the origin can.
//
// The sockets are native builtins, absent from the interpreter, so
// everything here is compiled for the host. The scenarios are
// e2eharness's, shared with the self-host twins.

// buildVCLBinary compiles one of the example's programs for the host.
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
