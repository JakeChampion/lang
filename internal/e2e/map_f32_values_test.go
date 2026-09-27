package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// conformance/cases/map_f32_column through native's -backend ssa, which the
// corpus legs do not reach (#10398).
func TestMapF32ColumnSSA(t *testing.T) {
	fern := buildFernCLI(t)
	src := filepath.Join(conformanceCases, "map_f32_column", "main.fern")
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	for _, c := range []struct {
		name, target string
		runnable     bool
		run          func(bin string) *exec.Cmd
	}{
		{"x86-64-ssa", "x86-64-linux", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"arm64-ssa", "arm64-linux", armok, func(bin string) *exec.Cmd { return runX86Bin(arm, bin) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.runnable {
				t.Skipf("no way to run %s binaries on this host", c.target)
			}
			bin := filepath.Join(t.TempDir(), "prog")
			if out, err := exec.Command(fern, "-target", c.target, "-backend", "ssa", "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := c.run(bin).CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "check=0" {
				t.Fatalf("got %q (%v), want check=0", out, err)
			}
		})
	}
}
