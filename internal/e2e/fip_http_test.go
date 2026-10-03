package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func runFipHttp(t *testing.T, fern, dir, variant string, runner []string) e2eharness.FipHttpReport {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "fip", "http_"+variant+".fern"))
	bin := filepath.Join(dir, "http_"+variant)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", variant, err, out)
	}
	out, err := benchX86Cmd(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", variant, err, out)
	}
	return e2eharness.ParseFipHttpReport(t, variant, out)
}

// The fip HTTP codec experiment (#9853), built by the Go compiler: see
// e2eharness.CheckFipHttpReports for what it pins.
func TestFipHttpCodecAgreesAndDoesNotAllocate(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()
	e2eharness.CheckFipHttpReports(t, runFipHttp(t, fern, dir, "baseline", runner), runFipHttp(t, fern, dir, "fip", runner))
}
