package e2eselfhost

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The self-host twins of internal/e2e's graceful-shutdown and worker
// tests (#9854): the same servers, compiled by the self-host compiler for
// x86-64, driven by the same client scenarios.

func selfHostServer(t *testing.T, src string) (bin string, runner []string) {
	t.Helper()
	gcc, runner, driverBin := buildModloadDriverX86(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("the server did not route through the IR path")
	}
	return buildBin(t, gcc, progDir, "serve", asm), runner
}

func selfHostShutdownServer(t *testing.T, port, drainMs int, entry string) (bin string, runner []string) {
	t.Helper()
	return selfHostServer(t, e2eharness.ServeShutdownSource(port, drainMs, entry))
}

func selfHostFreePort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	return port
}

func TestSelfHostServeShutdownDrainsAndExitsClean(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostShutdownServer(t, port, 5000, "tcp.tcp_serve_opts(%d, opts, handle)")
	cmd := binCmd(runner, bin)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckShutdownDrains(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeShutdownAbortsAtDrainDeadline(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostShutdownServer(t, port, 400, "tcp.tcp_serve_opts(%d, opts, handle)")
	cmd := binCmd(runner, bin)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckShutdownAbortsAtDrainDeadline(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostSupervisedServeForwardsShutdown(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostShutdownServer(t, port, 5000, "tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...opts, workers: 2 }, handle)")
	cmd := binCmd(runner, bin)
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckSupervisedShutdown(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestSelfHostServeInheritsListenFds(t *testing.T) {
	bin, runner := selfHostShutdownServer(t, 0, 5000, "tcp.tcp_serve_opts(%d, opts, handle)")
	addr, file := e2eharness.InheritedListener(t)
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), "LISTEN_FDS=1")
	e2eharness.StartServerProcess(t, cmd, file)
	e2eharness.CheckInheritedListener(t, cmd, addr)
}

func TestSelfHostSupervisedServeOneWorkerPerCPU(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.WorkersPerCPUServerSource(port))
	cmd := binCmd(runner, bin)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckWorkersPerCPU(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostSupervisedServeShutsDownAfterBurst(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.BurstServerSource(port))
	cmd := binCmd(runner, bin)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckShutdownAfterBurst(t, cmd, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeResponseRateCutsStalledReader(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.DataRateServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckResponseRateCutsStalledReader(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeResponseRateKeepsSteadyReader(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.DataRateServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckResponseRateKeepsSteadyReader(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeWithThreadedState(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.ThreadedStateServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckThreadedState(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeLargeResponse(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.LargeResponseServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckLargeResponse(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeRecvDeadline(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.RecvDeadlineServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckRecvDeadline(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostSupervisedServeWorkersServeSideBySide(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.WorkersServerSource(port))
	cmd := binCmd(runner, bin)
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckWorkersServeSideBySide(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestSelfHostSupervisedServeSurvivesHandlerTrap(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.TrappingServerSource(port))
	cmd := binCmd(runner, bin)
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckSurvivesHandlerTrap(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestSelfHostSupervisedServeCrashLoopGivesUp(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.TrappingServerSource(port))
	cmd := binCmd(runner, bin)
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckCrashLoopGivesUp(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// The self-host compiler synthesises the serve `main` of a handler
// program as the native checker does: with `init`'s state threaded, and
// for a bare `handle`.
func TestSelfHostServeInitProvidedState(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.InitStateServerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("PORT=%d", port))
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckInitState(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeHandleOnly(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.HandleOnlyServerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("PORT=%d", port))
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckHandleOnly(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeResultHandler(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.ResultHandlerServerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("PORT=%d", port))
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckResultHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeStatefulResultHandler(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.StatefulResultHandlerServerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("PORT=%d", port))
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckStatefulResultHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeShutdownHook(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.ShutdownHookServerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("PORT=%d", port))
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckShutdownHook(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestSelfHostServePerIPCap(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.PerIPCapServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckPerIPCap(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostServeAcceptDistribution(t *testing.T) {
	held := selfHostRaiseNofile(t, 4096+128) - 128
	if held > 4096 {
		held = 4096
	}
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.AcceptDistributionServerSource(port, 4))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckAcceptDistribution(t, fmt.Sprintf("127.0.0.1:%d", port), 4, held)
}

// selfHostRaiseNofile lifts the soft RLIMIT_NOFILE towards `want`, as far
// as the hard limit allows, and answers the soft limit in force.
func selfHostRaiseNofile(t *testing.T, want uint64) int {
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

func TestSelfHostSupervisedServeHandlerStallsItsWorker(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.StallServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckHandlerStallsItsWorker(t, fmt.Sprintf("127.0.0.1:%d", port))
}
