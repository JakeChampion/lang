package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The redesign's main benefit (docs/ASYNC-REDESIGN.md): two parallel
// outbound fetches that return their response BODIES, expressed purely
// through the supported combinator surface — `fetch.fetch_future` (the
// awaitable fetch) fanned out through `async.gather`. Both connections'
// reads overlap on one thread; each future's continuation recvs and
// returns the body. This is the edge-handler fan-out (fetch a cache + a
// primary, take both bodies) with NO `concurrent`/`await` keywords, no
// hand-rolled reactor, no `IoStep` — just `gather([fetch_future, …])`.
//
// A Go upstream answers both connections "hello-world"; exit 42 iff
// both bodies came back as expected. x86-64 + arm64 (arm64 under qemu
// connects to the host upstream).
func TestAsyncFetchFutureFanout(t *testing.T) {
	bin := buildFernCLI(t)
	port := e2eharness.StartBodyUpstream(t, []byte("hello-world"))
	src := e2eharness.FetchFutureFanoutSource(port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_future_fanout.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch_future.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: fetch_future + gather fan-out exit = %d, want 42", be.target, code)
			}
		})
	}
}

// fetch_future must read the WHOLE response, not just one recv buffer: the
// upstream returns a 10000-byte body (well past a single 4 KiB tcp_recv), so a
// single-recv read would truncate it. The streaming __fetch_drain chain
// re-suspends per chunk and accumulates, so http_body comes back at full
// length. Guest checks the body length == 10000 → exit 42.
func TestAsyncFetchFutureLargeBody(t *testing.T) {
	bin := buildFernCLI(t)
	const bodyLen = 10000
	port := e2eharness.StartBodyUpstream(t, bytes.Repeat([]byte("A"), bodyLen))
	src := e2eharness.FetchFutureLargeBodySource(port, bodyLen)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_future_big.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch_big.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: large-body fetch exit = %d, want 42 (truncated multi-chunk read?)", be.target, code)
			}
		})
	}
}
