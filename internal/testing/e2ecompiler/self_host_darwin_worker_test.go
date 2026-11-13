package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Each worker reports the pid of its process. An inline worker has pid 0.
const darwinWorkerPidsProg = `import "std/worker";

function work(n: i64): i64 {
  let t: i64 = 0;
  for i in 0..1000000 { t += i as i64 * n; }
  return t;
}

function main(): i32 {
  let a = worker.spawn((): i64 => work(1 as i64));
  let b = worker.spawn((): i64 => work(2 as i64));
  print(a.pid > 0 && b.pid > 0 && a.pid != b.pid);
  let x = a.join();
  let y = b.join();
  if (x == y) { return 1; }
  return 0;
}
`

// The Darwin forms of the worker gates: a worker is a process, the census
// balances, and no worker runs after its parent ends.
func TestSelfHostArm64DarwinWorkers(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	compile := func(t *testing.T, src string, env ...string) string {
		t.Helper()
		in := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(t.TempDir(), "prog")
		cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, in, e2eharness.SelfHostStdlibRoot(t))
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		return bin
	}
	leaks := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)

	t.Run("a worker is a process", func(t *testing.T) {
		out, err := exec.Command(compile(t, darwinWorkerPidsProg)).Output()
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			t.Fatalf("got %q, %v; want true", out, err)
		}
	})
	t.Run("worker_test with the census", func(t *testing.T) {
		src, err := os.ReadFile(e2eharness.RepoPath("tests", "stdlib", "worker_test.fern"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(compile(t, string(src), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v\n%s\n%s", err, &stdout, &stderr)
		}
		if m := leaks.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
			t.Fatalf("unbalanced census: %s", &stderr)
		}
	})
	t.Run("a body that frees a box of the parent", func(t *testing.T) {
		cmd := exec.Command(compile(t, workerImageFreeProg, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v\n%s", err, &stderr)
		}
		if m := leaks.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
			t.Fatalf("unbalanced census: %s", &stderr)
		}
	})
	bin := compile(t, workerOrphanProg)
	// The parent ends at once, so it races the watchdog. Each round is a new
	// draw of that race.
	for _, how := range []string{"exit", "abort"} {
		t.Run("the worker ends with its parent/"+how, func(t *testing.T) {
			for round := 0; round < 20; round++ {
				darwinWorkerEndsWithParent(t, bin, how == "abort")
			}
		})
	}
}

// darwinWorkerEndsWithParent runs workerOrphanProg once. Its stdout is a file,
// not a pipe, so a worker that outlives its parent fails the deadline instead
// of holding the read open.
func darwinWorkerEndsWithParent(t *testing.T, bin string, abort bool) {
	t.Helper()
	var args []string
	if abort {
		args = []string{"abort"}
	}
	stdout, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	cmd := exec.Command(bin, args...)
	cmd.Stdout = stdout
	_ = cmd.Run()
	out, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		t.Fatalf("no worker pid in %q", out)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker %d still runs 5 s after its parent ended", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
