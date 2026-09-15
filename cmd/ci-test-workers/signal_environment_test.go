//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Compare with the caller's dispositions, rather than assuming its shell has
// default signals. test2json ignores INT and QUIT in its own process; making
// it the test's parent must not silently change the test environment.
func TestWorkerSignalEnvironment(t *testing.T) {
	if _, err := exec.LookPath("gotestsum"); err != nil {
		t.Skip("gotestsum is required for the subprocess integration test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGQUIT} {
		t.Setenv("FERN_WORKER_SIGNAL_"+sig.String(), strconv.FormatBool(signal.Ignored(sig)))
	}
	var console bytes.Buffer
	c := config{binary: binary, output: filepath.Join(t.TempDir(), "results"), pattern: "^TestWorkerSignalFixture$", workers: 1, cpus: 1, timeout: time.Minute}
	if err := run(context.Background(), c, &console); err != nil {
		t.Fatalf("worker changed signal environment: %v\n%s", err, &console)
	}
}

func TestWorkerSignalFixture(t *testing.T) {
	if os.Getenv("FERN_WORKER_SIGNAL_"+os.Interrupt.String()) == "" {
		t.Skip("subprocess fixture")
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGQUIT} {
		want, err := strconv.ParseBool(os.Getenv("FERN_WORKER_SIGNAL_" + sig.String()))
		if err != nil {
			t.Fatal(err)
		}
		if got := signal.Ignored(sig); got != want {
			t.Errorf("%s ignored = %v, caller had %v", sig, got, want)
		}
	}
}

// The stdin converter reports success for this complete PASS stream. The
// runner must still report the binary's failure if it exits nonzero afterward.
func TestWorkerStreamExitStatus(t *testing.T) {
	if _, err := exec.LookPath("gotestsum"); err != nil {
		t.Skip("gotestsum is required for the subprocess integration test")
	}
	for _, code := range []int{0, 2} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "fixture")
			script := fmt.Sprintf("#!/bin/sh\nprintf '=== RUN   TestStreamFixture\\n--- PASS: TestStreamFixture (0.00s)\\nPASS\\n'\nexit %d\n", code)
			if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "events.jsonl")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var console bytes.Buffer
			err := runTestStream(ctx, config{binary: binary, timeout: time.Second}, []string{"TestStreamFixture"}, 1, 0, path, &console)
			if (err != nil) != (code != 0) {
				t.Fatalf("exit %d: run error = %v\n%s", code, err, &console)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verify(bytes.NewReader(data), []string{"TestStreamFixture"}); err != nil {
				t.Fatalf("expected a complete PASS stream independent of binary exit: %v\n%s", err, data)
			}
		})
	}
}

func TestWorkerStreamCancellation(t *testing.T) {
	if _, err := exec.LookPath("gotestsum"); err != nil {
		t.Skip("gotestsum is required for the subprocess integration test")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho started > \"$0.started\"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runTestStream(ctx, config{binary: binary, timeout: time.Minute}, []string{"TestStreamFixture"}, 1, 0, filepath.Join(dir, "events.jsonl"), &bytes.Buffer{})
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(binary + ".started"); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("worker stopped before the fixture started: %v", err)
		case <-ctx.Done():
			t.Fatal("fixture did not start before timeout")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled worker reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation left a test or formatter running")
	}
}
