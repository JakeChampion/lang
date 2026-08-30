package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- No examples program may die of a signal ---------------------------------
//
// `TestConformanceLeakCensusX86_64` fails when a fixture is killed by a
// signal, and that check is worth little on its own: measured against a
// compiler that segfaults `examples/proposals/unidiff.fern`, the
// conformance corpus stayed clean, because the crash was in `examples/`
// and that census covers `conformance/cases`.
//
// This runs the same check over the corpus where it bites. Against that
// same broken compiler it reports **crash=1**, and 0 on a good one — so
// unlike the conformance floor it catches the instance and not merely
// the shape.
//
// It matters because a LEAK GATE IS BLIND TO AN OVER-RELEASE. The
// attempted rc fix that produced that segfault was green on the leak
// census, `internal/ir`, and every rc e2e suite; the only things that
// saw it were the arm64 flat-vs-SSA differential and, now, this. Any
// change to reference counting can turn a leak into a use-after-free,
// and that failure has to be visible somewhere cheap.
//
// It gates on crashes ONLY, deliberately. Per-program leak counts over
// this corpus are not pinnable: the run carries listening servers and
// other programs that hit the timeout, and the count of those moved
// between two runs (7, then 8), so a pin on them would be flaky. The
// leak figures are logged for the record — 169 of 285 programs leaking
// 229,697 allocations, against the conformance corpus's 134 of 453 and
// 66,570 — and the deterministic conformance census keeps the pin.
//
// docs/rc-log/2026-08-30-conformance-leak-census.md.

// examplesCensusRunTimeout bounds one program. The corpus contains
// servers that never exit on their own; they are killed and counted as
// unmeasured, which is not a crash.
const examplesCensusRunTimeout = 10 * time.Second

func TestExamplesNoCrashX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the examples corpus; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	// The same corpus walk the arm64 differential uses, so the two agree
	// on what "the examples corpus" means and a program added to one is
	// added to both.
	corpus := arm64SSADiffCorpus(t)

	var mu sync.Mutex
	var crashed []string
	var ran, leaky, unpaired, unmeasured int
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, rel := range corpus {
		wg.Add(1)
		go func(rel string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, status := runExampleTraced(t, gcc, runner, rel)
			mu.Lock()
			defer mu.Unlock()
			switch status {
			case exampleCrashed:
				crashed = append(crashed, rel)
			case exampleUnmeasured:
				unmeasured++
			default:
				ran++
				if n > 0 {
					leaky++
					unpaired += n
				}
			}
		}(rel)
	}
	wg.Wait()

	t.Logf("%d programs: %d ran (%d leaking, %d unpaired allocs), %d unmeasured, %d crashed",
		len(corpus), ran, leaky, unpaired, unmeasured, len(crashed))

	if ran < 200 {
		t.Fatalf("only %d programs ran; this is measuring almost nothing", ran)
	}
	if len(crashed) > 0 {
		sort.Strings(crashed)
		t.Errorf("%d program(s) were killed by a signal: %s — an over-release reads as a crash, "+
			"and no leak gate can see it", len(crashed), strings.Join(crashed, ", "))
	}
}

type exampleStatus int

const (
	exampleRan exampleStatus = iota
	exampleCrashed
	exampleUnmeasured // did not build, or hit the timeout
)

// runExampleTraced emits one examples program with the heap tracer on,
// runs it under a timeout, and returns its unpaired allocation count.
func runExampleTraced(t *testing.T, gcc string, runner []string, rel string) (int, exampleStatus) {
	path := langSrcAbs(t, rel)
	prog, _, err := modload.Load(path)
	if err != nil {
		return 0, exampleUnmeasured
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return 0, exampleUnmeasured
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 0, exampleUnmeasured
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 0, exampleUnmeasured
	}
	asm, err := emitWithTracer(prog, info)
	if err != nil {
		return 0, exampleUnmeasured
	}
	dir, err := os.MkdirTemp("", "excensus")
	if err != nil {
		return 0, exampleUnmeasured
	}
	defer os.RemoveAll(dir)
	asmPath, binPath := filepath.Join(dir, "p.s"), filepath.Join(dir, "p")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return 0, exampleUnmeasured
	}
	if _, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		return 0, exampleUnmeasured
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &strings.Builder{}
	// Empty stdin rather than the test's: a filter program reading the
	// terminal would block until the timeout for no reason.
	cmd.Stdin = strings.NewReader("")
	if err := cmd.Start(); err != nil {
		return 0, exampleUnmeasured
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(examplesCensusRunTimeout):
		_ = cmd.Process.Kill()
		<-done
		return 0, exampleUnmeasured
	}
	if cmd.ProcessState.ExitCode() == -1 {
		return 0, exampleCrashed
	}
	n, _, err := pairRcTrace(stderr.String())
	if err != nil {
		return 0, exampleUnmeasured
	}
	return n, exampleRan
}
