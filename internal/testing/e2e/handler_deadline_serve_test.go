package e2e

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestHandlerDeadlineServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeHandlerDeadlineServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkHandlerDeadlineServe(t, addr, 0)
}

func TestHandlerDeadlineServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeHandlerDeadlineServeSource(t)
	out := filepath.Join(filepath.Dir(src), "handler_deadline_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, stderrPath := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkHandlerDeadlineServe(t, addr, 3*time.Second)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("GET /hangup HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	conn.(*net.TCPConn).SetLinger(0)
	conn.Close()
	e2eharness.WaitStderrContains(t, stderrPath, "hangup: cancelled before the deadline", 10*time.Second)
}

func writeHandlerDeadlineServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "handler_deadline_serve.fern")
	if err := os.WriteFile(src, []byte(handlerDeadlineServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const handlerDeadlineServeSource = `import "std/http";
import "std/platform";
import "std/serve";
import "std/time";

function bound(plat: platform.Host): string {
  match (plat.deadline()) {
    Some(at) => {
      let left: i64 = at - plat.elapsed_ns();
      if (left > 0 as i64 && left <= 1500000000 as i64) {
        return "bounded";
      }
      return "out of range";
    },
    None => {
      return "unbounded";
    }
  }
  return "";
}

function hangup(plat: platform.Host): HttpResponse {
  plat.sleep(time.duration_seconds(4 as i64));
  let early: boolean = false;
  match (plat.deadline()) {
    Some(at) => {
      early = plat.elapsed_ns() < at;
    },
    None => {}
  }
  if (plat.cancelled() && early) {
    plat.log("hangup: cancelled before the deadline");
  } else {
    plat.log("hangup: not cancelled early");
  }
  return http.ok("gone");
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/slow") {
    plat.sleep(time.duration_seconds(4 as i64));
    return http.ok("late");
  }
  if (req.path == "/spin") {
    let n: i64 = 0 as i64;
    while (!plat.cancelled()) {
      n = n + 1 as i64;
    }
    return http.ok("spun");
  }
  if (req.path == "/hangup") {
    return hangup(plat);
  }
  return http.ok(bound(plat) + " " + plat.cancelled().to_string());
}

function main(): i32 {
  return serve.exit_code(serve.run(0, serve.Config { ...serve.config(), handler_deadline: time.duration_millis(1500 as i64) }, handle));
}
`

func checkHandlerDeadlineServe(t *testing.T, addr string, within time.Duration) {
	t.Helper()
	cases := []struct{ path, want string }{
		{"/", "200 bounded false"},
		{"/spin", "503 "},
		{"/slow", "503 "},
	}
	for _, c := range cases {
		begun := time.Now()
		resp, err := http.Get("http://" + addr + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		took := time.Since(begun)
		if got := resp.Status[:3] + " " + string(body); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
		if within > 0 && c.path == "/slow" && took > within {
			t.Errorf("/slow answered after %v, want within %v", took, within)
		}
	}
}
