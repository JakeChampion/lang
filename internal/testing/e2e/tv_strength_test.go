package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/testing/fernsmith"
	"github.com/jakechampion/lang/internal/testing/tv"
)

const tvStrengthSeeds = 256

func TestDifferential_TranslationValidationStrength(t *testing.T) {
	z3 := tv.Z3(t)
	cli := e2eharness.SelfHostCLI(t)
	target := e2eharness.HostSelfHostTarget()
	count := uint64(tvStrengthSeeds)
	if v := os.Getenv("FERN_TV_SEEDS"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("FERN_TV_SEEDS=%q: %v", v, err)
		}
		count = n
	}
	seeds := diffOracleWindow(t, count)

	type found struct {
		seed  uint64
		pairs []tv.Pair
		err   error
	}
	results := make([]found, len(seeds))
	var wg sync.WaitGroup
	work := make(chan int)
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				pairs, err := strengthPairs(t, cli, target, fernsmith.GenMain(seeds[i]))
				results[i] = found{seed: seeds[i], pairs: pairs, err: err}
			}
		}()
	}
	for i := range seeds {
		work <- i
	}
	close(work)
	wg.Wait()

	var queries []string
	origin := map[string]uint64{}
	for _, r := range results {
		if r.err != nil {
			t.Errorf("seed %d: %v\nsrc:\n%s", r.seed, r.err, fernsmith.GenMain(r.seed))
			continue
		}
		for _, p := range r.pairs {
			q, err := tv.Encode(p)
			var m *tv.Mismatch
			if errors.As(err, &m) {
				t.Errorf("seed %d: strength reduction changed a value's shape: %s\nbefore: %s\nafter:  %s",
					r.seed, m.Reason, tv.Render(p.Before), tv.Render(p.After))
				continue
			}
			if err != nil {
				t.Fatalf("seed %d: %v", r.seed, err)
			}
			if _, ok := origin[q]; !ok {
				origin[q] = r.seed
				queries = append(queries, q)
			}
		}
	}
	answers, err := tv.Prove(z3, queries)
	if err != nil {
		t.Fatal(err)
	}
	proved := 0
	for i, a := range answers {
		switch a {
		case "unsat":
			proved++
		case "sat":
			model, err := tv.Counterexample(z3, queries[i])
			if err != nil {
				model = err.Error()
			}
			t.Errorf("seed %d: strength reduction is NOT equivalent; counterexample:\n%s\n%s\nsrc:\n%s",
				origin[queries[i]], queries[i], model, fernsmith.GenMain(origin[queries[i]]))
		default:
			t.Errorf("seed %d: z3 could not decide within its timeout:\n%s", origin[queries[i]], queries[i])
		}
	}
	t.Logf("%d programs, %d distinct rewritten runs, %d proved", len(seeds), len(queries), proved)
	if proved == 0 && !t.Failed() {
		t.Fatal("no rewrite was proved: the dump hook or the parser has stopped producing obligations, and the oracle would pass vacuously")
	}
}

func strengthPairs(t *testing.T, cli, target, src string) ([]tv.Pair, error) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(cli, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "prog.s"), srcPath, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.SelfHostChildEnv("FERN_TV_DUMP=strength")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("compile: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	rws, err := tv.Parse(stderr.String())
	if err != nil {
		return nil, err
	}
	var out []tv.Pair
	for _, rw := range rws {
		pairs, err := tv.Split(rw)
		if err != nil {
			return nil, fmt.Errorf("%s: %v\nbefore: %s\nafter:  %s", rw.Pass, err, tv.Render(rw.Before), tv.Render(rw.After))
		}
		out = append(out, pairs...)
	}
	return out, nil
}
