package e2eharness

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperServeEnv makes the test binary, run as a child, serve "ok" on the
// listener it inherits as descriptor 3 instead of running tests.
const helperServeEnv = "FERN_E2EHARNESS_HELPER_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(helperServeEnv) != "" {
		helperServe()
	}
	os.Exit(m.Run())
}

func helperServe() {
	if os.Getenv("LISTEN_FDS") != "1" {
		os.Stderr.WriteString("LISTEN_FDS is not 1\n")
		os.Exit(2)
	}
	ln, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(2)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}
		c.Write([]byte("ok"))
		c.Close()
	}
}

func TestStartInheritedServerHandsOverItsListener(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperServeEnv+"=1")
	addr, _ := StartInheritedServer(t, cmd)
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, 2)
	if _, err := c.Read(b); err != nil || string(b) != "ok" {
		t.Fatalf("read %q, %v; want the child's \"ok\"", b, err)
	}

	// The test's copy is closed, so once the child is gone nothing accepts.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("the listener still accepts after its server has gone: the test kept a copy")
	}
}

func TestReservedPortHoldsThePortForReusePortBinders(t *testing.T) {
	port := ReservedPort(t)
	if ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port))); err == nil {
		ln.Close()
		t.Fatalf("a plain listener bound reserved port %d", port)
	}
	fd, err := tcpSocket(syscall.AF_INET)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := bindEphemeral(fd, &syscall.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}, true); err != nil {
		t.Fatalf("an SO_REUSEPORT socket could not bind reserved port %d: %v", port, err)
	}
	if err := syscall.Listen(fd, 1); err != nil {
		t.Fatal(err)
	}
	// The reservation never listens, so a connection reaches the listener.
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the SO_REUSEPORT listener: %v", err)
	}
	c.Close()
}

func TestServerEndedSaysHowTheServerEnded(t *testing.T) {
	running := exec.Command("sleep", "30")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { running.Process.Kill(); running.Wait() }()
	if got := serverEnded(running); got != "was still running at cleanup" {
		t.Errorf("a running server: %q", got)
	}

	exited := exec.Command("sh", "-c", "exit 98")
	if err := exited.Start(); err != nil {
		t.Fatal(err)
	}
	got := serverEnded(exited)
	for deadline := time.Now().Add(10 * time.Second); got == "was still running at cleanup" && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		got = serverEnded(exited)
	}
	if got != "had exited: exit status 98" {
		t.Errorf("a server that exited: %q", got)
	}
}

func TestFileTailKeepsTheEnd(t *testing.T) {
	path := t.TempDir() + "/stderr.log"
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)+"serve: cannot listen"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := fileTail(path, 20)
	if !strings.HasSuffix(got, "serve: cannot listen") || !strings.HasPrefix(got, "... (100 bytes before)") {
		t.Errorf("fileTail = %q", got)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fileTail(path, 20); got != "(empty)" {
		t.Errorf("an empty stderr: %q", got)
	}
	if got := fileTail(path+".missing", 20); !strings.Contains(got, "no such file") {
		t.Errorf("a missing file: %q", got)
	}
}
