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

// The default worker count is one per processing unit (#9854).
func TestSupervisedServeOneWorkerPerCPU(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.WorkersPerCPUServerSource(port))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckWorkersPerCPU(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

// Every worker exits on SIGTERM after a burst of connections over the
// shared listener (#9854): a worker a wake-up reached without a
// connection left for it is not held in accept.
func TestSupervisedServeShutsDownAfterBurst(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.BurstServerSource(port))
	cmd, _ := startSupervisedServer(t, bin, runner)
	e2eharness.CheckShutdownAfterBurst(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

// The minimum data rate on the write side (#9854): a reader that stalls
// is cut off after the grace, one that keeps reading above the rate gets
// the whole response.
func TestServeResponseRateCutsStalledReaderX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.DataRateServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckResponseRateCutsStalledReader(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServeResponseRateKeepsSteadyReaderX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.DataRateServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckResponseRateKeepsSteadyReader(t, fmt.Sprintf("127.0.0.1:%d", port))
}
