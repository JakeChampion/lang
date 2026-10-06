package e2ecompiler

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twin of TestAsyncFetchFutureFanout: two `fetch_future`
// reads fanned out through `async.gather` against a loopback upstream,
// compiled by the self-host compiler and run.
func TestSelfHostFetchFutureFanoutX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	port := e2eharness.StartBodyUpstream(t, []byte("hello-world"))
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.FetchFutureFanoutSource(port))
	cmd := binCmd(runner, buildBin(t, gcc, progDir, "fetchfanout", asm))
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("fetch_future + gather fan-out exit = %d, want 42", code)
	}
}

// The self-host twin of TestAsyncFetchFutureLargeBody: a body past one
// receive buffer read whole by `fetch_future`.
func TestSelfHostFetchFutureLargeBodyX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	const bodyLen = 10000
	port := e2eharness.StartBodyUpstream(t, bytes.Repeat([]byte("A"), bodyLen))
	asm, progDir := compileSourceModload(t, runner, driverBin, e2eharness.FetchFutureLargeBodySource(port, bodyLen))
	cmd := binCmd(runner, buildBin(t, gcc, progDir, "fetchbig", asm))
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("large-body fetch exit = %d, want 42 (truncated multi-chunk read?)", code)
	}
}
