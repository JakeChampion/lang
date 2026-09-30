package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// tcp_serve_opts (#9853): the scenario is e2eharness's, shared with the
// self-host twin.
func TestServeOptionsX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ReusePortServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckReusePortReachesListener(t, port)
}

// A client past `max_connections_per_ip` (#9854) has its next connection
// closed as it is accepted, on the native backend and the interpreter;
// the scenario is e2eharness's, shared with the self-host twin.
func TestServePerIPCapX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.PerIPCapServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckPerIPCap(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServePerIPCapInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.PerIPCapServerSource(port)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start interp server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	e2eharness.CheckPerIPCap(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// `ServeOptions.limits` (#9854) lowers the parser's caps: past them a
// request is refused with the cap's status before the handler runs.
func TestServeLimitsX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.LimitsServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckServeLimits(t, fmt.Sprintf("127.0.0.1:%d", port))
}
