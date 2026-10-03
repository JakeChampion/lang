package e2eselfhost

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The per-held-connection bound of #9853: the serve loop compiled by the
// production self-host driver, with complete semantic lowering required,
// holds 64 idle connections, then
// 64 more, and the bump allocator's growth the second batch cost must be
// under 1 KiB per connection.
func TestSelfHostHeldConnectionsHeapBoundX86_64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires the Linux x86-64 native target")
	}
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("runs host-native only: the held connections are real sockets")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.HeldConnectionsServerSource(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "held")
	compile := exec.Command(driver, "-target", "x86-64-linux", "-o", bin, src, stdlib)
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1")
	report, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	e2eharness.RequireCompleteSemanticLowering(t, report)

	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	per, first, second := e2eharness.MeasureHeldConnections(t, addr)
	t.Logf("held connections: first batch of %d grew the heap by %d bytes, second by %d (%d per connection)",
		e2eharness.HeldConnectionsBatch, first, second, per)
	if per >= e2eharness.HeldConnectionsBytesPerConnection {
		t.Fatalf("holding %d more connections grew the heap by %d bytes per connection, want under %d",
			e2eharness.HeldConnectionsBatch, per, e2eharness.HeldConnectionsBytesPerConnection)
	}
}
