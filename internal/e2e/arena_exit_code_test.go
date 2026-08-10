package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
)

// Arena exhaustion exits 125, not 137.
//
// 137 is 128+9 — what a shell reports for a SIGKILL — so while __fern_alloc's
// bounds check used that status, a program exhausting its own fixed arena was
// indistinguishable from the kernel OOM-killer reaping it. The two have
// opposite causes and opposite fixes: an arena trap is a real, reproducible
// failure in the program (usually a leak) that will happen again on the next
// run; a SIGKILL means the HOST was short of RAM and the run should be retried
// with a smaller budget. Telling them apart cost a manual investigation every
// time, and three harness sites had given up and were treating any 137 as
// infra — which silently swallowed genuine compiler regressions.
//
// The value has to agree across FIVE emitters (two native backends, two
// self-host register backends, and the strbuf bounds trap), and nothing else
// would notice if one drifted: a wrong status still aborts the program, still
// prints the same stderr message, and only misleads the human reading the
// exit code weeks later. Hence this test.
//
// wasm is deliberately absent: it grows linear memory rather than trapping at
// a fixed arena, so it has no equivalent site.

func TestArenaExhaustedExitCodeIsNot137(t *testing.T) {
	// The whole point is that it cannot be confused with a signal death.
	// 128+N for N in 1..31 is the shell's signal-status range.
	for _, c := range []struct {
		name string
		got  int
	}{
		{"x86_64", x86_64.ExitArenaExhausted},
		{"arm64-linux", arm64.ExitArenaExhausted},
	} {
		if c.got == 137 {
			t.Errorf("%s: arena exhaustion is back to 137, which is SIGKILL's "+
				"status — the two become indistinguishable again", c.name)
		}
		if c.got >= 129 && c.got <= 159 {
			t.Errorf("%s: exit %d falls in the 128+signal range, so a signal "+
				"death can forge it", c.name, c.got)
		}
		if c.got >= 126 {
			t.Errorf("%s: exit %d is >= 126, which WASI refuses to carry — the "+
				"status would be reported as 1 through wasmtime", c.name, c.got)
		}
		if c.got <= 0 {
			t.Errorf("%s: exit %d would read as success", c.name, c.got)
		}
	}
	if x86_64.ExitArenaExhausted != arm64.ExitArenaExhausted {
		t.Errorf("native backends disagree: x86-64 exits %d, arm64 exits %d",
			x86_64.ExitArenaExhausted, arm64.ExitArenaExhausted)
	}
}

// TestArenaExhaustedExitCodeSelfHostLockstep reads the self-host emitters'
// sources and checks the status they write into their trap sequences matches
// the native constant. A source scan rather than a compile: building a
// self-host driver costs minutes, and the failure this guards against is
// somebody editing one emitter's literal and not the others — which a scan
// catches exactly as well.
func TestArenaExhaustedExitCodeSelfHostLockstep(t *testing.T) {
	want := x86_64.ExitArenaExhausted
	for _, c := range []struct {
		file  string
		trap  string // the register the exit status is moved into
		sites int
	}{
		// x86: `movq $<code>, %rdi` before `syscall` (exit = rax 60).
		{"asm_ir.fern", "%rdi", 2},
		// arm64: `mov x0, #<code>` before `svc #0` (exit = x8 93).
		{"asm_arm64_ir.fern", "x0", 2},
	} {
		path := filepath.Join("..", "..", "examples", "self_host", c.file)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		text := string(src)
		// Any remaining 137 in an exit-status position is a missed site.
		for _, stale := range []string{
			`movq $137, ` + c.trap,
			`mov ` + c.trap + `, #137`,
		} {
			if strings.Contains(text, stale) {
				t.Errorf("%s still exits 137 somewhere (%q) — SIGKILL's status; "+
					"every arena trap must use %d", c.file, stale, want)
			}
		}
		var marker string
		if c.trap == "%rdi" {
			marker = "movq $125, %rdi"
		} else {
			marker = "mov x0, #125"
		}
		if n := strings.Count(text, marker); n != c.sites {
			t.Errorf("%s has %d arena-trap exit sites emitting %q, want %d — "+
				"a site was added or removed without updating this count, so "+
				"one of them may be exiting with the wrong status",
				c.file, n, marker, c.sites)
		}
	}
}
