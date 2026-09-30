package e2e

import (
	"fmt"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Accept distribution (#9854): with a shared listener every worker watches
// exclusively, 4,096 held connections land on more than one worker, and
// the test logs how they spread. The soft descriptor limit is raised for
// the test process first, since the server inherits it and both sides
// need one descriptor per connection.
func TestServeAcceptDistributionX86_64(t *testing.T) {
	held := raiseNofile(t, 4096+128) - 128
	if held > 4096 {
		held = 4096
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.AcceptDistributionServerSource(port, 4))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckAcceptDistribution(t, fmt.Sprintf("127.0.0.1:%d", port), 4, held)
}

// raiseNofile lifts the soft RLIMIT_NOFILE towards `want`, as far as the
// hard limit allows, and answers the soft limit in force.
func raiseNofile(t *testing.T, want uint64) int {
	t.Helper()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	if lim.Cur < want {
		lim.Cur = want
		if lim.Cur > lim.Max {
			lim.Cur = lim.Max
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			t.Fatalf("setrlimit: %v", err)
		}
	}
	if lim.Cur < 256 {
		t.Skipf("the descriptor limit is %d; the measurement needs hundreds", lim.Cur)
	}
	return int(lim.Cur)
}
