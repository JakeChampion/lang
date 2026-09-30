// `tcp_serve_with` (#2679) — e2e for the state-threading serve loop on
// native x86-64; the scenario is e2eharness's, shared with the self-host
// twin.
//
// The state is a `Map[string, i32]`, deliberately: that is the case a
// closure-captured `Cell[T]` cannot cover, since E057 restricts a cell's
// element to cycle-free scalars and strings. The test pins the two
// properties threading is supposed to give — a value survives from one
// request to the next, and per-key state stays independent — plus the
// negative that makes them meaningful: a server that rebuilt its state
// each request would answer 1 every time.
package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestServeWithThreadedStateX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ThreadedStateServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckThreadedState(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// The same threading, with no `main` written at all (e2eharness's
// InitStateServerSource): what this pins over the test above is the
// wiring, that the synthesised main routes through tcp_serve_with and
// hands it init()'s value.
func TestServeInitProvidedStateX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.InitStateServerSource())
	startSupervisedServer(t, bin, runner, fmt.Sprintf("PORT=%d", port))
	e2eharness.CheckInitState(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// A handler failing with `dyn error.Error` (#9854): the error's message is
// logged through the platform and the answer is a bare 500.
func TestServeDynErrorHandlerX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.DynErrorHandlerServerSource())
	_, stderrPath := startSupervisedServer(t, bin, runner, fmt.Sprintf("PORT=%d", port))
	e2eharness.CheckDynErrorHandler(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// A handler answering a Result (#9854): the checker wraps it so `?`
// works in the handler and the failure is answered as a problem, with
// and without state.
func TestServeResultHandlerX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ResultHandlerServerSource())
	startSupervisedServer(t, bin, runner, fmt.Sprintf("PORT=%d", port))
	e2eharness.CheckResultHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServeStatefulResultHandlerX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.StatefulResultHandlerServerSource())
	startSupervisedServer(t, bin, runner, fmt.Sprintf("PORT=%d", port))
	e2eharness.CheckStatefulResultHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}
