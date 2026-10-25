package e2ecompiler

import (
	"net"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeListenFailure(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		held, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		port := held.Addr().(*net.TCPAddr).Port
		bin, runner := selfHostServer(t, e2eharness.ListenFailureServerSource(port, supervised))
		e2eharness.CheckListenFailure(t, binCmd(runner, bin), port)
		held.Close()
	}
}

func TestSelfHostServeListenErrorTyped(t *testing.T) {
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	bin, runner := selfHostServer(t, e2eharness.ListenErrorTypedSource(port))
	e2eharness.CheckListenErrorTyped(t, binCmd(runner, bin))
}
