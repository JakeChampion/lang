package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestAsyncWasmFetchFutureFanout(t *testing.T) {
	skipIfPreview2Missing(t)

	pSlow := e2eharness.StartDelayedUpstream(t, "AAA", 200*time.Millisecond)
	pFast := e2eharness.StartDelayedUpstream(t, "BBB", 10*time.Millisecond)

	compPath := buildResultComponent(t, e2eharness.WasmFetchFanoutSource)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PSLOW="+strconv.Itoa(pSlow), "--env", "PFAST="+strconv.Itoa(pFast), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	startT := time.Now()
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	elapsed := time.Since(startT)
	out := sout.String()

	ai := bytes.Index(sout.Bytes(), []byte("AAA"))
	bi := bytes.Index(sout.Bytes(), []byte("BBB"))
	if ai < 0 || bi < 0 {
		t.Fatalf("fan-out: missing a body in %q\nstderr:\n%s", out, serr.String())
	}
	if ai > bi {
		t.Errorf("fan-out: bodies not in input order (AAA after BBB) in %q", out)
	}
	if elapsed > 8*time.Second {
		t.Errorf("fan-out took %v — expected overlapped (~200ms + startup)", elapsed)
	}
}

func TestAsyncWasmRaceFetchDropsLoser(t *testing.T) {
	skipIfPreview2Missing(t)

	pA := e2eharness.StartDelayedUpstream(t, "AAA", 10*time.Millisecond)
	pB := e2eharness.StartDelayedUpstream(t, "BBB", 40*time.Millisecond)

	compPath := buildResultComponent(t, e2eharness.WasmRaceFetchSource)
	run := exec.Command("wasmtime", "run", "-S", "inherit-network",
		"--env", "PA="+strconv.Itoa(pA), "--env", "PB="+strconv.Itoa(pB), compPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run (race loser drop): %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	out := sout.String()
	if !bytes.Contains(sout.Bytes(), []byte("AAA")) && !bytes.Contains(sout.Bytes(), []byte("BBB")) {
		t.Errorf("race: want a winner body (AAA or BBB) in output, got %q\nstderr:\n%s", out, serr.String())
	}
}
