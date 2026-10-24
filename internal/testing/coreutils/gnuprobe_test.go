package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGnuVersionProbeIsBounded(t *testing.T) {
	dir := t.TempDir()
	writeProbe(t, dir, "#!/bin/sh\nwhile :; do echo 'not gnu at all'; done\n")

	done := make(chan struct{})
	var ver string
	var err error
	go func() {
		defer close(done)
		ver, err = gnuVersion(dir)
	}()

	select {
	case <-done:
	case <-time.After(gnuProbeTimeout + 20*time.Second):
		t.Fatalf("gnuVersion did not return for a yes(1) that never stops writing")
	}
	if err == nil {
		t.Fatalf("gnuVersion accepted a non-GNU yes as the reference: %q", ver)
	}
	if !strings.Contains(err.Error(), "not GNU coreutils") {
		t.Fatalf("error does not name the cause: %v", err)
	}
}

func TestGnuVersionProbeReadsTheFirstLine(t *testing.T) {
	dir := t.TempDir()
	writeProbe(t, dir, "#!/bin/sh\necho 'yes (GNU coreutils) 9.10'\nwhile :; do echo padding; done\n")

	ver, err := gnuVersion(dir)
	if err != nil {
		t.Fatalf("gnuVersion rejected a GNU version line: %v", err)
	}
	if ver != "yes (GNU coreutils) 9.10" {
		t.Fatalf("version = %q, want the first line", ver)
	}
}

func writeProbe(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "yes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
