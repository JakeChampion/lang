package e2e

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func runFipHttpApp(t *testing.T, fern, dir, variant string, runner []string) e2eharness.FipHttpAppReport {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "fip", "httpapp_"+variant+".fern"))
	bin := filepath.Join(dir, "httpapp_"+variant)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", variant, err, out)
	}
	out, err := benchX86Cmd(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", variant, err, out)
	}
	return e2eharness.ParseFipHttpAppReport(t, variant, out)
}

// The HTTP-like application pipeline experiment (#9586), built by the Go
// compiler: see e2eharness.CheckFipHttpAppReports for what it pins.
func TestFipHttpAppDisciplinesAgreeAndDoNotAllocate(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()
	e2eharness.CheckFipHttpAppReports(t,
		runFipHttpApp(t, fern, dir, "baseline", runner),
		runFipHttpApp(t, fern, dir, "fbip", runner),
		runFipHttpApp(t, fern, dir, "fip", runner))
}
