package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostModloadFoldsTargetOS(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("modload driver runs natively; skipping under an exec runner")
	}
	dir := writeSelfHostModloadProject(t)
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "target_os_modload")

	stage := t.TempDir()
	const src = `@noinline
function seven(): i32 {
    return 7;
}

function main(): i32 {
    if (target_os() == "linux") {
        return seven();
    }
    return 9;
}
`
	entry := filepath.Join(stage, "main.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("staging main.fern: %v", err)
	}

	t.Run("verifyprovided-clean", func(t *testing.T) {
		cmd := exec.Command(bin, entry, "-verifyprovided")
		out, _ := cmd.Output()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
			t.Fatalf("-verifyprovided reported problems on a target_os() program:\n%s", out)
		}
		if _, calls := parseProvidedTally(string(out)); calls < 1 {
			t.Errorf("resolved %d direct calls — a clean verdict over nothing proves nothing (out: %s)", calls, out)
		}
	})

	t.Run("emit-runs", func(t *testing.T) {
		asm := string(runDriverFile(t, runner, bin, entry))
		if len(asm) == 0 {
			t.Fatal("modload driver emitted 0 bytes")
		}
		for _, line := range strings.Split(asm, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "call") && strings.Contains(l, "target_os") {
				t.Fatalf("emitted asm still calls target_os: %q", l)
			}
		}
		progBin := buildBin(t, gcc, dir, "target_os_folded", asm)
		_, exit := hevRun(t, runner, progBin)
		if exit != 7 {
			t.Errorf("emitted program exited %d, want 7 — the fold did not reach the emit path", exit)
		}
	})
}
