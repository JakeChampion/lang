package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// conformance/cases/map_f32_column and map_f32_derived_key through native's
// -backend ssa, which the corpus legs do not reach (#10398). The derived-key
// case is the only one that reaches the struct-key get_or block's own f32
// conversion (#10458).
func TestMapF32ColumnSSA(t *testing.T) {
	fern := buildFernCLI(t)
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	for _, fixture := range []string{"map_f32_column", "map_f32_derived_key"} {
		src := filepath.Join(conformanceCases, fixture, "main.fern")
		for _, c := range []struct {
			name, target string
			runnable     bool
			run          func(bin string) *exec.Cmd
		}{
			{"x86-64-ssa", "x86-64-linux", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
			{"arm64-ssa", "arm64-linux", armok, func(bin string) *exec.Cmd { return runX86Bin(arm, bin) }},
		} {
			t.Run(fixture+"/"+c.name, func(t *testing.T) {
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
}
