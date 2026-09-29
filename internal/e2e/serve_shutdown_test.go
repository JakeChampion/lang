package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Graceful shutdown (#9854) on the native x86-64 backend; the scenarios
// are e2eharness's, shared with the self-host twins.

func TestServeShutdownDrainsAndExitsClean(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ServeShutdownSource(port, 5000, "tcp.tcp_serve_opts(%d, opts, handle)"))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckShutdownDrains(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServeShutdownAbortsAtDrainDeadline(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ServeShutdownSource(port, 400, "tcp.tcp_serve_opts(%d, opts, handle)"))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckShutdownAbortsAtDrainDeadline(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSupervisedServeForwardsShutdown(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ServeShutdownSource(port, 5000, "tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...opts, workers: 2 }, handle)"))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckSupervisedShutdown(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestServeInheritsListenFds(t *testing.T) {
	bin, runner := buildSupervisedServeBin(t, e2eharness.ServeShutdownSource(0, 5000, "tcp.tcp_serve_opts(%d, opts, handle)"))
	addr, file := e2eharness.InheritedListener(t)
	cmd := e2eharness.RunX86_64Bin(runner, bin)
	cmd.Env = append(cmd.Environ(), "LISTEN_FDS=1")
	e2eharness.StartServerProcess(t, cmd, file)
	e2eharness.CheckInheritedListener(t, cmd, addr)
}
