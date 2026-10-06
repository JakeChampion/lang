package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRExternProbe pins how `asm_ir_run -ir-probe` judges a call into
// a sibling module of a per-module build (#3451). The typed lowering checks a
// whole program, so the probe lowers the module together with the siblings
// `-ir-sigs` names: a call to a function one of them defines is produced, and a
// call to a name none of them defines is the checker's undefined-name error.
func TestSelfHostIRExternProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "airun")

	// probe runs `<driver> -ir-probe [extra...]` with prog on stdin.
	probe := func(t *testing.T, prog string, extra ...string) string {
		t.Helper()
		args := append([]string{"-ir-probe"}, extra...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q (extra %v): %v", prog, extra, err)
		}
		return string(out)
	}
	sibling := func(t *testing.T, name, src string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	prog := "function main(): i32 { return mystery(3); }"

	t.Run("sibling-call-is-produced", func(t *testing.T) {
		bail := probe(t, prog)
		if !strings.Contains(bail, "mystery") || !strings.HasSuffix(bail, "module: refused\n") {
			t.Errorf("without a sibling defining mystery: expected the checker's diagnostic / module: refused\n--- report ---\n%s", bail)
		}

		ok := probe(t, prog, "-ir-sigs", sibling(t, "mystery_lib.fern", "function mystery(n: i32): i32 { return n + 1; }"))
		if !strings.Contains(ok, "main: ir") || !strings.Contains(ok, "mystery: ir") || !strings.HasSuffix(ok, "module: IR\n") {
			t.Errorf("with -ir-sigs defining mystery: expected main: ir / module: IR\n--- report ---\n%s", ok)
		}
	})

	t.Run("an-unrelated-sibling-does-not-admit-it", func(t *testing.T) {
		rep := probe(t, prog, "-ir-sigs", sibling(t, "unrelated_lib.fern", "function unrelated(n: i32): i32 { return n; }"))
		if !strings.Contains(rep, "mystery") || !strings.HasSuffix(rep, "module: refused\n") {
			t.Errorf("a sibling defining an unrelated name should not admit mystery\n--- report ---\n%s", rep)
		}
	})
}
