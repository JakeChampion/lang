package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The per-held-connection heap bound of #9853 on the Go compiler's serve
// loop: 64 idle connections held, then 64 more, and the bump allocator's
// growth the second batch cost must be under 1 KiB per connection. The
// handler reports the high-water mark itself, so the measure is the
// server's own heap and not RSS.
func TestHeldConnectionsHeapBoundX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.HeldConnectionsServerSource(port))
	_, _ = startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	per, first, second := e2eharness.MeasureHeldConnections(t, addr)
	t.Logf("held connections: first batch of %d grew the heap by %d bytes, second by %d (%d per connection)",
		e2eharness.HeldConnectionsBatch, first, second, per)
	if per >= e2eharness.HeldConnectionsBytesPerConnection {
		t.Fatalf("holding %d more connections grew the heap by %d bytes per connection, want under %d",
			e2eharness.HeldConnectionsBatch, per, e2eharness.HeldConnectionsBytesPerConnection)
	}
}

// Keep-alive requests reuse what earlier ones freed (#9853): the bump
// high-water mark at request 200 is the one at request 2000.
func TestBumpPerRequestX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.BumpPerRequestServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckBumpPerRequest(t, fmt.Sprintf("127.0.0.1:%d", port))
}
