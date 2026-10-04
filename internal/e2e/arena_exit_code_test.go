package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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
// time, and three harness sites had defaulted to treating any 137 as
// infra — which silently hid genuine compiler regressions.
//
// The value has to agree across the emitters (the two register backends and
// the strbuf bounds trap), and nothing else would notice if one drifted: a
// wrong status still aborts the program, still prints the same stderr message,
// and only misleads the human reading the exit code weeks later. Hence this
// test.
//
// wasm is absent from the status check but not from the behaviour: it grows
// linear memory rather than trapping at a fixed arena, and a refused
// memory.grow raises `unreachable` inside __fern_alloc so the backtrace names
// the allocator (TestWASMHeapExhaustionTrapsInTheAllocator). Status parity
// with the constant needs wasi_proc_exit, which would put a WASI import into
// every allocating module — including the zero-import core ones — so it is
// deferred.

func TestArenaExhaustedExitCodeIsNot137(t *testing.T) {
	// The whole point is that it cannot be confused with a signal death.
	// 128+N for N in 1..31 is the shell's signal-status range.
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

// TestArenaExhaustedExitCodeSelfHostLockstep reads the self-host emitters'
// sources and checks the status they write into their trap sequences is
// e2eharness.ExitArenaExhausted. A source scan rather than a compile: the
// failure this guards against is somebody editing one emitter's literal and
// not the others — which a scan catches exactly as well.
func TestArenaExhaustedExitCodeSelfHostLockstep(t *testing.T) {
	want := e2eharness.ExitArenaExhausted
	for _, c := range []struct {
		file  string
		trap  string // the register the exit status is moved into
		sites int
	}{
		// x86: `movq $<code>, %rdi` before `syscall` (exit = rax 60).
		{"asm_ir.fern", "%rdi", 1},
		// arm64: `mov x0, #<code>` before `svc #0` (exit = x8 93).
		{"asm_arm64_ir.fern", "x0", 1},
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
			marker = fmt.Sprintf("movq $%d, %%rdi", want)
		} else {
			marker = fmt.Sprintf("mov x0, #%d", want)
		}
		if n := strings.Count(text, marker); n != c.sites {
			t.Errorf("%s has %d arena-trap exit sites emitting %q, want %d — "+
				"a site was added or removed without updating this count, so "+
				"one of them may be exiting with the wrong status",
				c.file, n, marker, c.sites)
		}
	}
}
