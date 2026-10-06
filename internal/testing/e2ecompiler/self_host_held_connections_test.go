package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The per-held-connection bound of #9853 and #9854: the serve loop
// compiled by the production self-host driver, with complete semantic
// lowering required, holds 64 idle connections, then 64 more, and the bump
// allocator's growth the second batch cost must be under 1 KiB per
// connection. "accepted" holds connections that never send a request;
// "kept" holds connections each left on keep-alive after one served
// request, each shape on a server of its own.
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
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []struct {
		name   string
		served bool
	}{{"accepted", false}, {"kept", true}} {
		t.Run(shape.name, func(t *testing.T) {
			addr := startHeldConnectionsServer(t, driver, stdlib, e2eharness.HeldConnectionsServerSource)
			per, first, second := e2eharness.MeasureHeldConnections(t, addr, shape.served)
			t.Logf("%s connections: first batch of %d grew the heap by %d bytes, second by %d (%d per connection)",
				shape.name, e2eharness.HeldConnectionsBatch, first, second, per)
			if per >= e2eharness.HeldConnectionsBytesPerConnection {
				t.Fatalf("holding %d more %s connections grew the heap by %d bytes per connection, want under %d",
					e2eharness.HeldConnectionsBatch, shape.name, per, e2eharness.HeldConnectionsBytesPerConnection)
			}
		})
	}
	// The P3 shape (docs/NET-P3-SUSPENSION-PLAN.md §4, slice 5): every held
	// connection carries a request whose handler is parked on the upstream,
	// so the growth is a suspended handler's — its flight, its saved frames
	// and its fetch client's connection and buffers.
	t.Run("suspended", func(t *testing.T) {
		up := e2eharness.StartFetchUpstream(t)
		e2eharness.SetFetchProxy(t, up)
		addr := startHeldConnectionsServer(t, driver, stdlib, e2eharness.HeldSuspendedServerSource)
		per, first, second := e2eharness.MeasureHeldSuspended(t, addr, up)
		t.Logf("suspended handlers: first batch of %d grew the heap by %d bytes, second by %d (%d per handler)",
			e2eharness.HeldConnectionsBatch, first, second, per)
		if per >= e2eharness.HeldSuspendedBytesPerHandler {
			t.Fatalf("holding %d more suspended handlers grew the heap by %d bytes per handler, want under %d",
				e2eharness.HeldConnectionsBatch, per, e2eharness.HeldSuspendedBytesPerHandler)
		}
	})
}

// startHeldConnectionsServer compiles the server source with the
// self-host driver, starts it, and answers its address.
func startHeldConnectionsServer(t *testing.T, driver, stdlib string, source func() string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(source()), 0o644); err != nil {
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

	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin))
	return addr
}
