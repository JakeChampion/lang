package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const pipelinedBurstSource = `import "std/http";
import "std/platform";
import "std/serve";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  return http.ok(req.path);
}

function main(): i32 {
  return serve.run(0, serve.Config { ...serve.config(), keep_alive_requests: 100000 }, handle);
}
`

// TestSelfHostServePipelinedBurst pipelines more requests than the
// io_uring's buffer ring holds (docs/NET-P7-IO-URING-PLAN.md slice 3, 256
// buffers of 4 KiB): one connection sends 40,000 requests, some 1.4 MB,
// beside three that send 8,000 each, every write racing the reads, and
// every answer must come back in order. Under epoll and under the ring,
// where the loop reads through provided buffers and falls back to reading
// the socket when they run out.
func TestSelfHostServePipelinedBurst(t *testing.T)     { runPipelinedBurst(t, false) }
func TestSelfHostServePipelinedBurstRing(t *testing.T) { runPipelinedBurst(t, true) }

func runPipelinedBurst(t *testing.T, ring bool) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(pipelinedBurstSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1"))
	if ring {
		cmd.Env = append(os.Environ(), "FERN_IO_URING=1")
	}
	addr, stderr := e2eharness.StartInheritedServer(t, cmd)
	counts := []int{40000, 8000, 8000, 8000}
	errs := make(chan error, len(counts))
	for i, n := range counts {
		go func(conn, n int) { errs <- pipelineOn(addr, conn, n) }(i, n)
	}
	for range counts {
		if err := <-errs; err != nil {
			got, _ := os.ReadFile(stderr)
			t.Fatalf("%v\nserver stderr:\n%s", err, got)
		}
	}
}

func pipelineOn(addr string, conn, n int) error {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(120 * time.Second))
	go func() {
		w := bufio.NewWriterSize(c, 64<<10)
		for i := 0; i < n; i++ {
			fmt.Fprintf(w, "GET /c%d/r%d HTTP/1.1\r\nHost: x\r\n\r\n", conn, i)
		}
		w.Flush()
	}()
	r := bufio.NewReader(c)
	for i := 0; i < n; i++ {
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			return fmt.Errorf("connection %d: response %d: %v", conn, i, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if want := fmt.Sprintf("/c%d/r%d", conn, i); err != nil || string(body) != want {
			return fmt.Errorf("connection %d: response %d is %q (%v), want %q", conn, i, body, err, want)
		}
	}
	return nil
}
