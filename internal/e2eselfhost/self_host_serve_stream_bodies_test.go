package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The streamed request body's gate (docs/NET-P3-SUSPENSION-PLAN.md §3.9):
// e2eharness.StreamBodiesServerSource under the self-host compiler, where
// a handler's pull parks, so an upload in progress holds nothing — /ok on
// another connection is answered while the body is still arriving — and
// the sequential checks hold as well: a chunked upload with a request
// pipelined behind it, a stalled body answered 408, a chunk past the cap
// answered 413, Expect: 100-continue invited only when the handler reads,
// a client gone mid-body. The Go compiler's blocking fallback runs the
// sequential checks alone (internal/e2e TestServeStreamBodiesSequential).
func TestSelfHostServeStreamBodies(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.StreamBodiesServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.CheckStreamBodiesOverlap(t, addr)
	e2eharness.CheckStreamBodiesSequential(t, addr)
}

func TestSelfHostServeStreamBodiesArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.StreamBodiesServerSource(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eharness.StartServerProcess(t, runArm64Bin(qemu, cli.arm64Binary(t, src)))
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.CheckStreamBodiesOverlap(t, addr)
	e2eharness.CheckStreamBodiesSequential(t, addr)
}
