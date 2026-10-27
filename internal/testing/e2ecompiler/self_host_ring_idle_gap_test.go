//go:build linux && amd64

package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostServeRingIdleGap(t *testing.T) {
	t.Setenv("FERN_IO_URING", "1")
	const rounds = 64
	cli := buildNativeSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(port, rounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := traceSyscalls(t, exec.Command(cli.nativeBinary(t, src, "FERN_STRICT_IR=1")))
	get := e2eharness.KeepAliveGET(t, fmt.Sprintf("127.0.0.1:%d", port))
	for i := 0; i < rounds; i++ {
		get("/")
	}
	before := tr.snapshot()
	start := time.Now()
	for i := 0; i < rounds; i++ {
		get("/")
		// Leave a gap after the application consumes each receive buffer.
		time.Sleep(10 * time.Millisecond)
	}
	seconds := int64(time.Since(start).Seconds()) + 2
	after := tr.snapshot()
	if err := syscall.Kill(tr.pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := tr.wait(t); code != 0 {
		t.Fatalf("server exited %d on SIGTERM, want 0", code)
	}
	// A fallback to epoll cannot satisfy this positive ring-entry count.
	entries := int64(after[426] - before[426])
	if entries < rounds-1 {
		t.Fatalf("only %d ring entries over %d requests; the ring path must run", entries, rounds)
	}
	var total int64
	for nr, n := range after {
		total += int64(n - before[nr])
	}
	want := int64(2 * rounds)
	slack := serveSyscallsSlackPerSecond * seconds
	t.Logf("%d syscalls over %d delayed requests, including %d ring entries:\n%s", total, rounds, entries, syscallBreakdown(before, after))
	if total < want-slack || total > want+slack {
		t.Fatalf("%d syscalls over %d delayed requests, want %d within unchanged slack %d:\n%s", total, rounds, want, slack, syscallBreakdown(before, after))
	}
	if n := after[syscall.SYS_CLOCK_GETTIME] - before[syscall.SYS_CLOCK_GETTIME]; n != 0 {
		t.Errorf("the ring serve loop made %d clock_gettime syscalls", n)
	}
}
