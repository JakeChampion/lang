package e2ecompiler

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func selfHostWasmFetch(t *testing.T, src string, env ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	path, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", bin, path, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component build: %v\n%s", err, out)
	}
	args := []string{"run", "-S", "inherit-network"}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, "wasmtime", append(args, bin)...)
	var sout, serr bytes.Buffer
	run.Stdout, run.Stderr = &sout, &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.Bytes(), serr.Bytes())
	}
	return sout.Bytes()
}

func TestSelfHostWasmFetchFutureFanout(t *testing.T) {
	pSlow := e2eharness.StartDelayedUpstream(t, "AAA", 200*time.Millisecond)
	pFast := e2eharness.StartDelayedUpstream(t, "BBB", 10*time.Millisecond)
	out := selfHostWasmFetch(t, e2eharness.WasmFetchFanoutSource,
		"PSLOW="+strconv.Itoa(pSlow), "PFAST="+strconv.Itoa(pFast))
	ai, bi := bytes.Index(out, []byte("AAA")), bytes.Index(out, []byte("BBB"))
	if ai < 0 || bi < 0 || ai > bi {
		t.Fatalf("fan-out: want AAA then BBB, got %q", out)
	}
}

func TestSelfHostWasmRaceFetchDropsLoser(t *testing.T) {
	pA := e2eharness.StartDelayedUpstream(t, "AAA", 10*time.Millisecond)
	pB := e2eharness.StartDelayedUpstream(t, "BBB", 40*time.Millisecond)
	out := selfHostWasmFetch(t, e2eharness.WasmRaceFetchSource,
		"PA="+strconv.Itoa(pA), "PB="+strconv.Itoa(pB))
	if !bytes.Contains(out, []byte("AAA")) && !bytes.Contains(out, []byte("BBB")) {
		t.Fatalf("race: want a winner body (AAA or BBB), got %q", out)
	}
}
