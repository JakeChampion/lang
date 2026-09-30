//go:build linux

package e2eselfhost

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostServeNoDelay(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.NoDelayServerSource(port))
	cmd := binCmd(runner, bin)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckServeNoDelay(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostNetNoDelay(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.NetNoDelayProbe())
	cmd := binCmd(runner, bin)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckNetNoDelay(t, cmd, out)
}
