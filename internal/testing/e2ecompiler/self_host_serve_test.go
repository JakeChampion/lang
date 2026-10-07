package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twins of internal/testing/e2e's graceful-shutdown and worker
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

func selfHostShutdownServer(t *testing.T, drainMs int, entry string) (bin string, runner []string) {
	t.Helper()
	return selfHostServer(t, e2eharness.ServeShutdownSource(drainMs, entry))
}

func TestSelfHostServeShutdownDrainsAndExitsClean(t *testing.T) {
	bin, runner := selfHostShutdownServer(t, 5000, "serve.run(0, opts, handle)")
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckShutdownDrains(t, cmd, addr)
}

func TestSelfHostServeShutdownAbortsAtDrainDeadline(t *testing.T) {
	bin, runner := selfHostShutdownServer(t, 400, "serve.run(0, opts, handle)")
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckShutdownAbortsAtDrainDeadline(t, cmd, addr)
}

func TestSelfHostSupervisedServeForwardsShutdown(t *testing.T) {
	bin, runner := selfHostShutdownServer(t, 5000, "serve.supervise(0, serve.Config { ...opts, workers: 2 }, handle)")
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckSupervisedShutdown(t, cmd, addr, stderrPath)
}

func TestSelfHostServeInheritsListenFds(t *testing.T) {
	bin, runner := selfHostShutdownServer(t, 5000, "serve.run(0, opts, handle)")
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckInheritedListener(t, cmd, addr)
}

func TestSelfHostSupervisedServeOneWorkerPerCPU(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.WorkersPerCPUServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckWorkersPerCPU(t, cmd, addr)
}

func TestSelfHostSupervisedServeShutsDownAfterBurst(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.BurstServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckShutdownAfterBurst(t, cmd, addr)
}

func TestSelfHostSupervisedServeWorkersStopWithSupervisor(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.OrphanedWorkersServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckWorkersStopWithSupervisor(t, cmd, addr)
}

func TestSelfHostWatchParentGone(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ParentGoneProbe())
	cmd := binCmd(runner, bin)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckParentGone(t, cmd, out)
}

func TestSelfHostServeResponseRateCutsStalledReader(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.DataRateServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckResponseRateCutsStalledReader(t, addr)
}

func TestSelfHostServeResponseRateKeepsSteadyReader(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.DataRateServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckResponseRateKeepsSteadyReader(t, addr)
}

func TestSelfHostServeWithThreadedState(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ThreadedStateServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckThreadedState(t, addr)
}

func TestSelfHostServeLargeResponse(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.LargeResponseServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckLargeResponse(t, addr)
}

func TestSelfHostServeRecvDeadline(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.RecvDeadlineServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckRecvDeadline(t, addr)
}

func TestSelfHostServeIncompleteRequestAtEOF(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.RecvDeadlineServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckIncompleteRequestAtEOF(t, addr)
}

func TestSelfHostSupervisedServeWorkersServeSideBySide(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.WorkersServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckWorkersServeSideBySide(t, addr, stderrPath)
}

func TestSelfHostServeHandlersOverlap(t *testing.T) {
	up := e2eharness.StartFetchUpstream(t)
	e2eharness.SetFetchProxy(t, up)
	bin, runner := selfHostServer(t, e2eharness.BlockingHandlerServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckHandlersOverlap(t, addr)
}

func TestSelfHostSupervisedServeReusePortWorkers(t *testing.T) {
	port := e2eharness.ReservedPort(t)
	bin, runner := selfHostServer(t, e2eharness.ReusePortWorkersServerSource(port))
	cmd := binCmd(runner, bin)
	stderrPath := e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckOwnListenersSurviveHandlerTrap(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

func TestSelfHostSupervisedServeSurvivesHandlerTrap(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.TrappingServerSource(0))
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckSurvivesHandlerTrap(t, addr, stderrPath)
}

func TestSelfHostSupervisedServeCrashLoopGivesUp(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	bin, runner := selfHostServer(t, e2eharness.TrappingServerSource(0))
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckCrashLoopGivesUp(t, cmd, addr, stderrPath)
}

func TestSelfHostSupervisedServeCrashLoopStopsSurvivor(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	bin, runner := selfHostServer(t, e2eharness.StalledSurvivorServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckCrashLoopStopsSurvivor(t, cmd, addr, stderrPath)
}

func TestSelfHostSupervisedServeTrapThenShutdownExitsClean(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.TrappingServerSource(0))
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckTrapThenShutdownExitsClean(t, cmd, addr, stderrPath)
}

func TestSelfHostServeConfig(t *testing.T) {
	port := e2eharness.ReservedPort(t)
	bin, runner := selfHostServer(t, e2eharness.ReusePortServerSource(port))
	e2eharness.StartServerProcess(t, binCmd(runner, bin))
	e2eharness.CheckReusePortReachesListener(t, port)
}

func TestSelfHostServeMaxConnectionsFloor(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.MaxConnectionsFloorServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckMaxConnectionsFloor(t, addr)
}

// The self-host compiler synthesises the serve `main` of a handler
// program as the native checker does: with `init`'s state threaded, and
// for a bare `handle`.
func TestSelfHostServeInitProvidedState(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.InitStateServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckInitState(t, addr)
}

func TestSelfHostServeInitConfigAliased(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.InitStateAliasedServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckInitState(t, addr)
}

func TestSelfHostServeHandleOnly(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.HandleOnlyServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckHandleOnly(t, addr)
}

func TestSelfHostServeDynErrorHandler(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.DynErrorHandlerServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckDynErrorHandler(t, addr, stderrPath)
}

func TestSelfHostServeDynErrorHandlerAliased(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.DynErrorHandlerAliasedServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckDynErrorHandler(t, addr, stderrPath)
}

func TestSelfHostServeResultHandler(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ResultHandlerServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckResultHandler(t, addr)
}

func TestSelfHostServeStatefulResultHandler(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.StatefulResultHandlerServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckStatefulResultHandler(t, addr)
}

// A target without processes gets the single-process serve loop from the
// synthesised main, as native's TestHandlerKindsMatchWhatTheCompilerAccepts
// pins: the wasm32-wasi build of a handle-only program reaches
// `serve.run` and never the supervisor.
func TestSelfHostWasiCliHandlerProgramBuilds(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(prog, []byte(e2eharness.HandleOnlyServerSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	wat := filepath.Join(dir, "handler.wat")
	if msg, err := exec.Command(cli, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI: %v\n%s", err, msg)
	}
	text, err := os.ReadFile(wat)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`serve__run\b`).Match(text) || strings.Contains(string(text), "__supervise") {
		t.Fatal("the wasm32-wasi handler program does not serve through serve.run alone")
	}
}

func TestSelfHostServeShutdownHook(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ShutdownHookServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckShutdownHook(t, cmd, addr, stderrPath)
}

func TestSelfHostServeShutdownHookSigint(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ShutdownHookServerSource())
	cmd := binCmd(runner, bin)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckShutdownHookOnSigint(t, cmd, addr, stderrPath)
}

func TestSelfHostServePerIPCap(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.PerIPCapServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckPerIPCap(t, addr)
}

func TestSelfHostServeFileBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "served.txt")
	if err := os.WriteFile(path, []byte(e2eharness.FileBodyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, runner := selfHostServer(t, e2eharness.FileBodyServerSource(path))
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckFileBody(t, addr)
}

func TestSelfHostServeBinaryBody(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.BinaryBodyServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckBinaryBody(t, addr)
}

func TestSelfHostServeResponseFields(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ResponseFieldsServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckResponseFields(t, addr)
}

func TestSelfHostServeStreamingBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, e2eharness.StreamingBodyContent(), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, runner := selfHostServer(t, e2eharness.StreamingBodyServerSource(path))
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckStreamingBody(t, addr)
}

func TestSelfHostServeAcceptDistribution(t *testing.T) {
	held := selfHostRaiseNofile(t, 4096+128) - 128
	if held > 4096 {
		held = 4096
	}
	bin, runner := selfHostServer(t, e2eharness.AcceptDistributionServerSource(4))
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckAcceptDistribution(t, addr, 4, held)
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
	bin, runner := selfHostServer(t, e2eharness.StallServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckHandlerStallsItsWorker(t, addr)
}

func TestSelfHostFetchDeadline(t *testing.T) {
	silentPort, livePort := e2eharness.FetchDeadlineUpstreams(t)
	bin, runner := selfHostServer(t, e2eharness.FetchDeadlineSource(silentPort, livePort))
	e2eharness.CheckFetchDeadline(t, binCmd(runner, bin))
}

func TestSelfHostServeLimits(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.LimitsServerSource())
	cmd := binCmd(runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeLimits(t, addr)
}
