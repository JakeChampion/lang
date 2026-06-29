package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The redesign's headline payoff (docs/ASYNC-REDESIGN.md): two parallel
// outbound fetches that return their response BODIES, expressed purely
// through the blessed combinator surface — `fetch.fetch_future` (the
// awaitable fetch) fanned out through `async.gather`. Both connections'
// reads overlap on one thread; each future's continuation recvs and
// returns the body. This is the edge-handler fan-out (fetch a cache + a
// primary, take both bodies) with NO `concurrent`/`await` keywords, no
// hand-rolled reactor, no `IoStep` — just `gather([fetch_future, …])`.
//
// A Go upstream answers both connections "hello-world"; exit 42 iff
// both bodies came back as expected. x86-64 + arm64 (arm64 under qemu
// connects to the host upstream).
func TestAsyncFetchFutureFanout(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 256)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello-world")
			}(conn)
		}
	}()

	// 127.0.0.1 in network byte order: 127 | (1 << 24).
	const host = 127 | (1 << 24)
	src := fmt.Sprintf(`import "std/async";
import "std/fetch";

function main(): i32 {
    var f1: async.Future[string] = fetch.fetch_future(%d, %d, "/1");
    var f2: async.Future[string] = fetch.fetch_future(%d, %d, "/2");
    var fs: async.Future[string][] = [f1, f2];
    var bodies: string[] = async.gather(fs, "");
    if (bodies[0] == "hello-world" && bodies[1] == "hello-world") { return 42; }
    return 85;
}`, host, port, host, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fetch_future_fanout.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64", x86QemuOrEmpty, runX86Bin},
		{"arm64", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_fetch_future.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: fetch_future + gather fan-out exit = %d, want 42", be.target, code)
			}
		})
	}
}
