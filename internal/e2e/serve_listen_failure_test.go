package e2e

import (
	"net"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A serve entry that cannot bind its port (#9853) says which address and
// why, and exits 98, through the single loop and through the supervisor.
func TestServeListenFailureX86_64(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		held, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		port := held.Addr().(*net.TCPAddr).Port
		bin, runner := buildSupervisedServeBin(t, e2eharness.ListenFailureServerSource(port, supervised))
		e2eharness.CheckListenFailure(t, e2eharness.RunX86_64Bin(runner, bin), port)
		held.Close()
	}
}
