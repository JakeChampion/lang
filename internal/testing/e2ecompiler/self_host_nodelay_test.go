//go:build linux

package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeNoDelay(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.NoDelayServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeNoDelay(t, cmd, addr)
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

func TestSelfHostServeCorksBurst(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.NoDelayServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeCorksBurst(t, cmd, addr)
}
