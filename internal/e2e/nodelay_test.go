//go:build linux

package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TCP_NODELAY is on by default (#9853): on every connection the serve
// loop accepts, and on both ends std/net's `connect` and `accept` make.
// The option is read back from the running process's own sockets.
func TestServeNoDelayX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.NoDelayServerSource(port))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckServeNoDelay(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestNetNoDelayX86_64(t *testing.T) {
	bin, runner := buildSupervisedServeBin(t, e2eharness.NetNoDelayProbe())
	cmd := e2eharness.RunX86_64Bin(runner, bin)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckNetNoDelay(t, cmd, out)
}

// Responses corked per readable event (#9854): a burst of 32 pipelined
// requests is answered in one write, counted in the server's own segments.
func TestServeCorksBurstX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.NoDelayServerSource(port))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckServeCorksBurst(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}
