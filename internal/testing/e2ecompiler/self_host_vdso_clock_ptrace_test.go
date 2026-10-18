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

// The two gates here trace a program natively and count what it asks the
// kernel. They run on the host's own ISA only: under qemu the guest vDSO's
// clock_gettime is a trampoline that makes the syscall, so an emulated leg
// would count one per read whatever the runtime did.

// TestSelfHostVdsoClockNoSyscall traces the clock program and wants no
// clock_gettime from it: every read went through the vDSO.
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

// serveSyscallsPerRequest pins what the serve loop asks the kernel per hello
// request on a keep-alive connection: the reactor wait, the read and the
// send. The clocks it reads between them go through the vDSO, so none of
// them is a syscall; before #12537 they were eight more.
const serveSyscallsPerRequest = 3

// serveSyscallsRounds is how many requests the count spans, after
// serveSyscallsWarm have brought the connection's buffers to size; both
// run under ptrace, which stops the server twice per syscall.
const serveSyscallsRounds = 5000
const serveSyscallsWarm = 1000

// serveSyscallsSlackPerSecond is how far the total may sit from a whole
// number per request, per second the rounds took and two more, for what
// the loop does once a second.
const serveSyscallsSlackPerSecond = 4

// TestSelfHostServeSyscallsPerRequest traces the hello server and counts
// the syscalls it enters per request, none of them clock_gettime. The
// counts are read after a response arrives both times, so a wait the server
// has entered and not yet left counts the same at both.
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

// serveFetchSyscallsPerRequest is what the two-fetch server
// (e2eharness.ServeFetchAllocsServerSource) enters per request on a
// keep-alive connection: the hello request's three, and three per upstream
// request over the pooled connection, `sendto`, the reactor wait the
// handler's task parks in, and `read`. The pooled socket stays watched
// between requests (`async.KEEP`), so no `epoll_ctl` re-arms it.
const serveFetchSyscallsPerRequest = 9

// TestSelfHostServeSyscallsPerRequestTwoFetch traces the two-fetch server,
// its upstream a Go server reached through `http_proxy`, and counts the
// syscalls it enters per request.
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

// syscallBreakdown lists, by syscall number, how many of each were entered
// between two snapshots.
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

// syscallTracer runs one process under ptrace, on a thread of its own, and
// counts each syscall it enters by number.
type syscallTracer struct {
	pid     int
	started chan struct{}
	done    chan struct{}
	err     error
	code    int
	mu      sync.Mutex
	counts  map[uint64]int
}

// traceSyscalls starts cmd under ptrace with its stderr kept for the test's
// failure output, and returns once the tracee is past its exec.
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

// run is the tracer's loop. Every ptrace request must come from the thread
// that started the tracee, so it locks one for the tracee's lifetime.
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
	// A syscall stop then reports SIGTRAP|0x80, so no signal the tracee
	// receives can be mistaken for one.
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

// snapshot is a copy of the counts so far.
func (tr *syscallTracer) snapshot() map[uint64]int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	counts := make(map[uint64]int, len(tr.counts))
	for nr, n := range tr.counts {
		counts[nr] = n
	}
	return counts
}

// wait returns the tracee's exit code, failing the test when it died of a
// signal, the tracer failed, or a minute passes.
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
