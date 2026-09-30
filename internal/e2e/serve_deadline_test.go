// HTTP/TCP deadlines (#4385) — e2e tests for the read-deadline
// surface on the native x86-64 backend:
//
//  1. `tcp_serve_deadline`'s slow-loris guard: a client that
//     connects and trickles a partial header is disconnected at
//     the per-request read deadline WITHOUT a response, and the
//     single-threaded accept loop is not pinned — a well-formed
//     request right after still answers 200 (the scenario is
//     e2eharness's, shared with the self-host twin).
//  2. `fetch_get_deadline`: against an upstream that accepts and
//     never replies the call returns `None` at the deadline;
//     against a live upstream it returns `Some(response)` (also
//     e2eharness's, with a self-host twin).
//
// The interp fallback (poll is a stub there, so the deadline
// degrades to blocking reads) is covered by
// TestSupervisedServeInterpFallback, which serves through the
// same deadline-gated loop under -interp.
package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestServeRecvDeadlineX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.RecvDeadlineServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckRecvDeadline(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestFetchDeadlineX86_64(t *testing.T) {
	silentPort, livePort := e2eharness.FetchDeadlineUpstreams(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.FetchDeadlineSource(silentPort, livePort))
	e2eharness.CheckFetchDeadline(t, e2eharness.RunX86_64Bin(runner, bin))
}
