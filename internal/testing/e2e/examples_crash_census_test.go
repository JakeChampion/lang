package e2e

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// --- No examples program may die of a signal ---------------------------------
//
// `TestConformanceLeakCensusX86_64` fails when a fixture is killed by a
// signal, but a crash can live outside `conformance/cases`: an attempted rc
// fix once segfaulted `tests/proposals/unidiff.fern` while that corpus
// stayed clean. This runs the same check over the examples corpus, compiled
// by the self-host with the heap tracer on.
//
// A LEAK GATE IS BLIND TO AN OVER-RELEASE, and any change to reference
// counting can turn a leak into a use-after-free, so that failure has to be
// visible somewhere cheap.
//
// It gates on crashes ONLY. Per-program leak counts over this corpus are not
// pinnable: it carries listening servers and other programs that hit the
// timeout, and how many do moves between runs. The leak figures are logged
// for the record; the conformance census keeps the pin.
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
	_, runner := x86_64Tooling(t)
	// Built here, not in a worker: a first build that fails reports through t.
	e2eharness.SelfHostCLI(t)
	e2eharness.SelfHostStdlibRoot(t)
	corpus := examplesCorpus(t)

	var mu sync.Mutex
	var crashed []string
	var leaky []string
	var ran, unpaired, unmeasured int
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, rel := range corpus {
		wg.Add(1)
		go func(rel string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, status := runExampleTraced(t, runner, rel)
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
					leaky = append(leaky, fmt.Sprintf("%s (%d)", rel, n))
					unpaired += n
				}
			}
		}(rel)
	}
	wg.Wait()

	sort.Strings(leaky)
	t.Logf("%d programs: %d ran (%d leaking, %d unpaired allocs), %d unmeasured, %d crashed",
		len(corpus), ran, len(leaky), unpaired, unmeasured, len(crashed))
	if len(leaky) > 0 {
		t.Logf("leaking: %s", strings.Join(leaky, ", "))
	}

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

// runExampleTraced compiles one examples program with the heap tracer on,
// runs it under a timeout, and returns its unpaired allocation count.
func runExampleTraced(t *testing.T, runner []string, rel string) (int, exampleStatus) {
	dir, err := os.MkdirTemp("", "excensus")
	if err != nil {
		return 0, exampleUnmeasured
	}
	defer os.RemoveAll(dir)
	bin, err := buildTracedX86_64(t, langSrcAbs(t, rel), dir)
	if err != nil {
		return 0, exampleUnmeasured
	}
	cmd := runX86_64Bin(runner, bin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &strings.Builder{}
	// Empty stdin rather than the test's: a filter program reading the
	// terminal would block until the timeout for no reason.
	cmd.Stdin = strings.NewReader("")
	if timedOut, _ := runBounded(cmd, examplesCensusRunTimeout); timedOut || cmd.ProcessState == nil {
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
