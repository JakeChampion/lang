package e2ecompiler

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
