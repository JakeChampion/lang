//go:build linux && (amd64 || arm64)

package e2ecompiler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostVdsoClockNoSyscall(t *testing.T) {
	cli := buildNativeSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(vdsoClockSource), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := traceSyscalls(t, exec.Command(cli.nativeBinary(t, src)))
	if code := tr.wait(t); code != 7 {
		t.Fatalf("traced clock program exited %d, want 7", code)
	}
	if n := tr.snapshot()[syscall.SYS_CLOCK_GETTIME]; n != 0 {
		t.Errorf("the clocks made %d clock_gettime syscalls", n)
	}
}

const serveSyscallsPerRequest = 3

const serveSyscallsRounds = 5000
const serveSyscallsWarm = 1000

const serveSyscallsSlackPerSecond = 4

func TestSelfHostServeSyscallsPerRequest(t *testing.T) {
	cli := buildNativeSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(port, serveSyscallsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := traceSyscalls(t, exec.Command(cli.nativeBinary(t, src, "FERN_STRICT_IR=1")))
	get := e2eharness.KeepAliveGET(t, fmt.Sprintf("127.0.0.1:%d", port))
	for i := 0; i < serveSyscallsWarm; i++ {
		get("/")
	}
	before := tr.snapshot()
	start := time.Now()
	for i := 0; i < serveSyscallsRounds; i++ {
		get("/")
	}
	seconds := int64(time.Since(start).Seconds()) + 2
	after := tr.snapshot()
	if err := syscall.Kill(tr.pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := tr.wait(t); code != 0 {
		t.Errorf("server exited %d on SIGTERM, want 0", code)
	}
	if n := after[syscall.SYS_CLOCK_GETTIME] - before[syscall.SYS_CLOCK_GETTIME]; n != 0 {
		t.Errorf("the serve loop made %d clock_gettime syscalls over %d requests; the clocks read through the vDSO", n, serveSyscallsRounds)
	}
	var total int64
	for nr, n := range after {
		total += int64(n - before[nr])
	}
	got := (total + serveSyscallsRounds/2) / serveSyscallsRounds
	if d := total - got*serveSyscallsRounds; d < -serveSyscallsSlackPerSecond*seconds || d > serveSyscallsSlackPerSecond*seconds {
		t.Fatalf("%d syscalls over %d requests is %d from a whole number per request:\n%s", total, serveSyscallsRounds, d, syscallBreakdown(before, after))
	}
	if got != serveSyscallsPerRequest {
		t.Errorf("the serve loop makes %d syscalls per hello request; pinned %d.\n"+
			"Above the pin is a regression. Below it, lower the pin in the same change.\n%s", got, serveSyscallsPerRequest, syscallBreakdown(before, after))
	}
}

const serveFetchSyscallsPerRequest = 9

func TestSelfHostServeSyscallsPerRequestTwoFetch(t *testing.T) {
	cli := buildNativeSelfHostCLI(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "upstream")
	}))
	t.Cleanup(up.Close)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeFetchAllocsServerSource(port, serveSyscallsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli.nativeBinary(t, src, "FERN_STRICT_IR=1"))
	cmd.Env = append(os.Environ(), "http_proxy=http://u@"+up.Listener.Addr().String(), "no_proxy=")
	tr := traceSyscalls(t, cmd)
	get := e2eharness.KeepAliveGET(t, fmt.Sprintf("127.0.0.1:%d", port))
	for i := 0; i < serveSyscallsWarm; i++ {
		get("/")
	}
	before := tr.snapshot()
	start := time.Now()
	for i := 0; i < serveSyscallsRounds; i++ {
		get("/")
	}
	seconds := int64(time.Since(start).Seconds()) + 2
	after := tr.snapshot()
	if err := syscall.Kill(tr.pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := tr.wait(t); code != 0 {
		t.Errorf("server exited %d on SIGTERM, want 0", code)
	}
	var total int64
	for nr, n := range after {
		total += int64(n - before[nr])
	}
	got := (total + serveSyscallsRounds/2) / serveSyscallsRounds
	if d := total - got*serveSyscallsRounds; d < -serveSyscallsSlackPerSecond*seconds || d > serveSyscallsSlackPerSecond*seconds {
		t.Fatalf("%d syscalls over %d requests is %d from a whole number per request:\n%s", total, serveSyscallsRounds, d, syscallBreakdown(before, after))
	}
	if got != serveFetchSyscallsPerRequest {
		t.Errorf("the two-fetch server makes %d syscalls per request; pinned %d.\n"+
			"Above the pin is a regression. Below it, lower the pin in the same change.\n%s", got, serveFetchSyscallsPerRequest, syscallBreakdown(before, after))
	}
}

func syscallBreakdown(before, after map[uint64]int) string {
	var nrs []uint64
	for nr := range after {
		if after[nr] != before[nr] {
			nrs = append(nrs, nr)
		}
	}
	sort.Slice(nrs, func(i, j int) bool { return nrs[i] < nrs[j] })
	var b strings.Builder
	for _, nr := range nrs {
		fmt.Fprintf(&b, "  syscall %d: %d\n", nr, after[nr]-before[nr])
	}
	return b.String()
}

type syscallTracer struct {
	pid     int
	started chan struct{}
	done    chan struct{}
	err     error
	code    int
	mu      sync.Mutex
	counts  map[uint64]int
}

func traceSyscalls(t *testing.T, cmd *exec.Cmd) *syscallTracer {
	t.Helper()
	stderrPath := filepath.Join(t.TempDir(), "stderr.log")
	errFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = errFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Ptrace: true}
	tr := &syscallTracer{started: make(chan struct{}), done: make(chan struct{}), counts: map[uint64]int{}}
	go tr.run(cmd)
	<-tr.started
	if tr.err != nil {
		t.Fatal(tr.err)
	}
	t.Cleanup(func() {
		select {
		case <-tr.done:
		default:
			_ = syscall.Kill(tr.pid, syscall.SIGKILL)
			<-tr.done
		}
		errFile.Close()
		if t.Failed() {
			out, _ := os.ReadFile(stderrPath)
			t.Logf("traced program's stderr:\n%s", out)
		}
	})
	return tr
}

func (tr *syscallTracer) run(cmd *exec.Cmd) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(tr.done)
	if err := cmd.Start(); err != nil {
		tr.err = err
		close(tr.started)
		return
	}
	tr.pid = cmd.Process.Pid
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(tr.pid, &ws, 0, nil); err != nil {
		tr.err = err
		close(tr.started)
		return
	}
	if err := syscall.PtraceSetOptions(tr.pid, syscall.PTRACE_O_TRACESYSGOOD); err != nil {
		tr.err = err
		close(tr.started)
		return
	}
	close(tr.started)
	entering := true
	signal := 0
	for {
		if err := syscall.PtraceSyscall(tr.pid, signal); err != nil {
			tr.err = err
			return
		}
		if _, err := syscall.Wait4(tr.pid, &ws, 0, nil); err != nil {
			tr.err = err
			return
		}
		if ws.Exited() {
			tr.code = ws.ExitStatus()
			return
		}
		if ws.Signaled() {
			tr.err = fmt.Errorf("traced program died of %v", ws.Signal())
			return
		}
		signal = 0
		if ws.StopSignal() != syscall.SIGTRAP|0x80 {
			signal = int(ws.StopSignal())
			continue
		}
		if entering {
			var regs syscall.PtraceRegs
			if err := syscall.PtraceGetRegs(tr.pid, &regs); err != nil {
				tr.err = err
				return
			}
			nr := syscallNumber(&regs)
			tr.mu.Lock()
			tr.counts[nr]++
			tr.mu.Unlock()
		}
		entering = !entering
	}
}

func (tr *syscallTracer) snapshot() map[uint64]int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	counts := make(map[uint64]int, len(tr.counts))
	for nr, n := range tr.counts {
		counts[nr] = n
	}
	return counts
}

func (tr *syscallTracer) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(time.Minute):
		t.Fatal("traced program did not exit within a minute")
	}
	if tr.err != nil {
		t.Fatal(tr.err)
	}
	return tr.code
}
