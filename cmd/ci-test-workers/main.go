// ci-test-workers runs disjoint top-level tests in isolated processes within
// one CI job. Process isolation protects packages with mutable global state.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	binary, output, pattern string
	workers, cpus           int
	timeout                 time.Duration
}

type result struct {
	Worker   int               `json:"worker"`
	Tests    []string          `json:"tests"`
	CPUs     int               `json:"cpus"`
	Seconds  float64           `json:"seconds"`
	Outcomes map[string]string `json:"outcomes,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// Serialize gotestsum diagnostics from the workers so they can share the job
// console without racing the writer.
type lockedWriter struct {
	sync.Mutex
	w io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.w.Write(p)
}

func main() {
	var c config
	flag.StringVar(&c.binary, "binary", "", "absolute path of a compiled Go test binary")
	flag.StringVar(&c.output, "output", "", "new directory for inventories and test JSON")
	flag.StringVar(&c.pattern, "run", "", "top-level test selection regular expression")
	flag.IntVar(&c.workers, "workers", 2, "number of isolated processes")
	flag.IntVar(&c.cpus, "cpus", runtime.GOMAXPROCS(0), "total CPU budget")
	flag.DurationVar(&c.timeout, "timeout", 10*time.Minute, "test timeout per worker")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, terminationSignal())
	defer cancel()
	if err := run(ctx, c, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ci-test-workers:", err)
		os.Exit(1)
	}
}

func inventory(output []byte, workers int) ([][]string, error) {
	names := strings.Fields(string(output))
	if workers < 1 || len(names) < workers {
		return nil, fmt.Errorf("%d tests cannot fill %d workers", len(names), workers)
	}
	groups := make([][]string, workers)
	seen := make(map[string]bool)
	for i, name := range names {
		if !token.IsIdentifier(name) || !strings.HasPrefix(name, "Test") || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate test name %q", name)
		}
		seen[name] = true
		groups[i%workers] = append(groups[i%workers], name)
	}
	return groups, nil
}

func exactPattern(names []string) string {
	escaped := make([]string, len(names))
	for i, name := range names {
		escaped[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

func verify(r io.Reader, expected []string) (map[string]string, error) {
	wanted := make(map[string]bool, len(expected))
	for _, name := range expected {
		wanted[name] = true
	}
	started := make(map[string]bool)
	outcomes := make(map[string]string)
	packagePassed := false
	decoder := json.NewDecoder(r)
	for {
		var event struct{ Action, Test string }
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return outcomes, fmt.Errorf("invalid test JSON: %w", err)
		}
		if event.Test == "" {
			switch event.Action {
			case "fail", "skip":
				return outcomes, fmt.Errorf("test package reported %s", event.Action)
			case "pass":
				if packagePassed {
					return outcomes, errors.New("duplicate package outcome")
				}
				packagePassed = true
			}
			continue
		}
		parent, _, _ := strings.Cut(event.Test, "/")
		if !wanted[parent] {
			return outcomes, fmt.Errorf("unexpected test %s", event.Test)
		}
		switch event.Action {
		case "run":
			if started[event.Test] {
				return outcomes, fmt.Errorf("duplicate test start %s", event.Test)
			}
			started[event.Test] = true
		case "pass", "skip", "fail":
			if !started[event.Test] || outcomes[event.Test] != "" {
				return outcomes, fmt.Errorf("missing start or duplicate outcome for %s", event.Test)
			}
			outcomes[event.Test] = event.Action
			if event.Action == "fail" {
				return outcomes, fmt.Errorf("failed test %s", event.Test)
			}
		}
	}
	for _, name := range expected {
		if outcomes[name] == "" {
			return outcomes, fmt.Errorf("missing test outcome %s", name)
		}
	}
	for name := range started {
		if outcomes[name] == "" {
			return outcomes, fmt.Errorf("unfinished test %s", name)
		}
	}
	if !packagePassed {
		return outcomes, errors.New("missing successful package outcome")
	}
	return outcomes, nil
}

func run(ctx context.Context, c config, output io.Writer) error {
	if !filepath.IsAbs(c.binary) || c.output == "" || c.pattern == "" || c.workers < 1 || c.cpus < c.workers || c.timeout <= 0 {
		return errors.New("require absolute binary, new output directory, run pattern, positive timeout and 1 <= workers <= cpus")
	}
	if _, err := regexp.Compile(c.pattern); err != nil {
		return err
	}
	// Bound inventory too: a package TestMain may execute before -test.list.
	ctx, cancel := context.WithTimeout(ctx, c.timeout+30*time.Second)
	defer cancel()
	list := exec.CommandContext(ctx, c.binary, "-test.list="+c.pattern)
	isolate(list)
	listed, err := list.Output()
	if err != nil {
		return fmt.Errorf("list tests: %w", err)
	}
	groups, err := inventory(listed, c.workers)
	if err != nil {
		return err
	}
	if err := os.Mkdir(c.output, 0o755); err != nil {
		return err
	}
	console := &lockedWriter{w: output}
	results := make([]result, c.workers)
	var wg sync.WaitGroup
	for i, names := range groups {
		wg.Go(func() {
			cpus := c.cpus / c.workers
			if i < c.cpus%c.workers {
				cpus++
			}
			r := result{Worker: i, Tests: names, CPUs: cpus}
			path := filepath.Join(c.output, fmt.Sprintf("worker-%d.jsonl", i))
			cmd := exec.CommandContext(ctx, "gotestsum", "--format", "pkgname-and-test-fails", "--jsonfile", path, "--raw-command", "--",
				"go", "tool", "test2json", "-t", "-p", fmt.Sprintf("worker-%d", i), c.binary,
				"-test.run="+exactPattern(names), "-test.v=test2json", "-test.count=1",
				"-test.timeout="+c.timeout.String(), "-test.parallel="+strconv.Itoa(cpus))
			cmd.Env = append(os.Environ(), "GOMAXPROCS="+strconv.Itoa(cpus))
			cmd.Stdout, cmd.Stderr = console, console
			isolate(cmd)
			start := time.Now()
			runErr := cmd.Run()
			r.Seconds = time.Since(start).Seconds()
			f, readErr := os.Open(path)
			if readErr == nil {
				r.Outcomes, readErr = verify(f, names)
				readErr = errors.Join(readErr, f.Close())
			}
			if err := errors.Join(runErr, readErr); err != nil {
				r.Error = err.Error()
			}
			results[i] = r
		})
	}
	wg.Wait()
	var failures []error
	for _, r := range results {
		fmt.Fprintf(console, "worker %d: %d tests, %d CPUs, %.3fs, error=%q\n", r.Worker, len(r.Tests), r.CPUs, r.Seconds, r.Error)
		if r.Error != "" {
			failures = append(failures, fmt.Errorf("worker %d: %s", r.Worker, r.Error))
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(c.output, "summary.json"), append(data, '\n'), 0o644)
	}
	return errors.Join(append(failures, err)...)
}
