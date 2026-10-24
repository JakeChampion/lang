package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestArenaExhaustedExitCodeIsNot137(t *testing.T) {
	got := e2eharness.ExitArenaExhausted
	if got == 137 {
		t.Errorf("arena exhaustion is back to 137, which is SIGKILL's " +
			"status — the two become indistinguishable again")
	}
	if got >= 129 && got <= 159 {
		t.Errorf("exit %d falls in the 128+signal range, so a signal "+
			"death can forge it", got)
	}
	if got >= 126 {
		t.Errorf("exit %d is >= 126, which WASI refuses to carry — the "+
			"status would be reported as 1 through wasmtime", got)
	}
	if got <= 0 {
		t.Errorf("exit %d would read as success", got)
	}
}

func TestArenaExhaustedExitCodeSelfHostLockstep(t *testing.T) {
	want := e2eharness.ExitArenaExhausted
	marker := fmt.Sprintf("aborts.oom(), %d)", want)
	for _, file := range []string{"asm_ir.fern", "asm_arm64_ir.fern"} {
		path := filepath.Join("..", "..", "..", "compiler", file)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(src)
		for _, stale := range []string{`movq $137, %rdi`, `mov x0, #137`, `, 137)`} {
			if strings.Contains(text, stale) {
				t.Errorf("%s still exits 137 somewhere (%q) — SIGKILL's status; "+
					"every arena trap must use %d", file, stale, want)
			}
		}
		if n := strings.Count(text, marker); n != 1 {
			t.Errorf("%s has %d arena traps passing %q, want 1 — a site was "+
				"added or removed, so one of them may be exiting with the "+
				"wrong status", file, n, marker)
		}
		unmapped := map[string]string{
			"asm_ir.fern":       fmt.Sprintf("movq $%d, %%rdi", e2eharness.ExitArenaUnmapped),
			"asm_arm64_ir.fern": fmt.Sprintf("mov x0, #%d", e2eharness.ExitArenaUnmapped),
		}[file]
		if !strings.Contains(text, unmapped) || !strings.Contains(text, ".Lalloc_map_fail") {
			t.Errorf("%s: no .Lalloc_map_fail trap exiting %d (%q)", file, e2eharness.ExitArenaUnmapped, unmapped)
		}
	}
	if e2eharness.ExitArenaUnmapped == want || e2eharness.ExitArenaUnmapped == e2eharness.ExitSanitizer ||
		e2eharness.ExitArenaUnmapped >= 126 || e2eharness.ExitArenaUnmapped <= 0 {
		t.Errorf("ExitArenaUnmapped %d collides with another runtime status or leaves 1..125", e2eharness.ExitArenaUnmapped)
	}
}
