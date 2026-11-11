package e2e

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/corpus"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const examplesCensusRunTimeout = 10 * time.Second

func TestExamplesNoCrashX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the examples corpus; not a -short test")
	}
	_, runner := x86_64Tooling(t)
	e2eharness.SelfHostCLI(t)
	e2eharness.SelfHostStdlibRoot(t)
	all := examplesCorpus(t)
	programs, err := corpus.Shard(all, os.Getenv("FERN_EXAMPLES_CENSUS_SHARD"))
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var crashed []string
	var leaky []string
	var ran, unpaired, unmeasured int
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, rel := range programs {
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
		len(programs), ran, len(leaky), unpaired, unmeasured, len(crashed))
	if len(leaky) > 0 {
		t.Logf("leaking: %s", strings.Join(leaky, ", "))
	}

	// A shard must run its share of the 200 programs that the full corpus must run.
	if minRan := 200 * len(programs) / len(all); ran < minRan {
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
	exampleUnmeasured
)

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
