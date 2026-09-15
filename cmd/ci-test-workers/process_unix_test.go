//go:build linux || darwin

package main

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"
)

type readyWriter struct {
	once  sync.Once
	ready chan struct{}
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(p), nil
}

func TestCancellationKillsDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & echo ready; wait")
	isolate(cmd)
	writer := &readyWriter{ready: make(chan struct{})}
	cmd.Stdout = writer
	finished := make(chan error, 1)
	go func() { finished <- cmd.Run() }()
	select {
	case <-writer.ready:
	case err := <-finished:
		t.Fatalf("process exited before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("process did not become ready")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled command passed")
		}
	case <-time.After(2 * time.Second):
		// The sleep inherits stdout. Killing only its shell leaves the
		// pipe open and forces Wait to hit its longer WaitDelay.
		t.Fatal("descendant kept the output pipe open after cancellation")
	}
}
