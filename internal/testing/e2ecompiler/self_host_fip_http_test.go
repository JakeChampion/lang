package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twin of TestFipHttpCodecAgreesAndDoesNotAllocate: the fip
// HTTP codec experiment (#9853) built by the self-host CLI, held to the same
// claims (e2eharness.CheckFipHttpReports).
func TestSelfHostFipHttpCodecAgreesAndDoesNotAllocate(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	run := func(variant string) e2eharness.FipHttpReport {
		t.Helper()
		src := filepath.Join(repoRootFromTest(t), "examples", "fip", "http_"+variant+".fern")
		bin := filepath.Join(dir, "http_"+variant)
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", bin, src, cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v\n%s", variant, err, out)
		}
		out, err := runX86_64Bin(cli.runner, bin).CombinedOutput()
		if err != nil {
			t.Fatalf("run %s: %v\n%s", variant, err, out)
		}
		return e2eharness.ParseFipHttpReport(t, variant, out)
	}
	e2eharness.CheckFipHttpReports(t, run("baseline"), run("fip"))
}
