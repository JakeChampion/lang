package e2eselfhost

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestSelfHostWasiHttpOutgoing is the gate for std/fetch on the
// `wasm32-wasi-http` target, where the client sends through the host's
// wasi:http/outgoing-handler: a handler compiled by the self-host answers
// `/run` under `wasmtime serve` with one line per case against the scripted
// loopback origin, the same lines TestFetchClient's program prints for the
// cases the hosted route shares (e2eharness.FetchHostedSource says which it
// does not). The native compiler composes no such handler: its wasm backend
// does not lower std/wasi_http's externs, and the client is self-host-first.
func TestSelfHostWasiHttpOutgoing(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	up := e2eharness.StartFetchUpstream(t)
	closed := e2eharness.ClosedLoopbackPort(t)
	dir := t.TempDir()
	component := compileWasiHttp(t, dir, e2eharness.FetchHostedSource(up.Port, closed), "hosted.wasm")
	if out, err := exec.Command(wasmtools, "validate", component).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	status, _, body := serveComponent(t, wasmtime, component, "GET", "/run", "")
	if status != 200 {
		t.Fatalf("GET /run = %d\n%s", status, body)
	}
	e2eharness.CheckFetchHosted(t, up, body)
}
