package e2eselfhost

import (
	"fmt"
	"net"
	"strings"
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
