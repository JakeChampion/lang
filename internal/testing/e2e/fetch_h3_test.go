package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/fetch against std/serve with HTTP/3 on: the first request goes over
// TCP and reads the Alt-Svc field the server sends, the pool then offers
// the origin's HTTP/3 port, and the requests after it ride QUIC, one of
// them a POST with a body, leaving an HTTP/3 connection kept.
func TestFetchH3X86_64(t *testing.T) {
	w := newServeH3World(t)
	out := filepath.Join(w.dir, "serve_h3.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	w.driveFetch(t, runX86Bin(w.qemu, out))
}

func TestFetchH3Interp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	w := newServeH3WorldWithConfig(t, "serve.Config { ...serve.config(), recv_deadline: time.duration_seconds(60 as i64) }")
	w.driveFetch(t, exec.Command(bin, "-interp", w.src))
}

// std/fetch against std/serve with a pool that keeps no connection: the
// second HTTP/3 GET resumes the session with the first one's ticket and
// goes in 0-RTT, which the server marks with Early-Data. The POST after it
// resumes too, but waits for the handshake.
func TestFetchH3ZeroRttX86_64(t *testing.T) {
	w := newServeH3World(t)
	out := filepath.Join(w.dir, "serve_h3.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	cmd := runX86Bin(w.qemu, out)
	clientSrc := filepath.Join(w.dir, "fetch_h3_early.fern")
	if err := os.WriteFile(clientSrc, []byte(fetchH3EarlySource), 0o644); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(w.dir, "fetch_h3_early.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("client build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runX86Bin(w.qemu, client, port, w.ca).CombinedOutput()
	if err != nil {
		t.Fatalf("fetch client: %v\n%s", err, got)
	}
	want := `GET /hello -> 200 hello /hello
GET /a -> 200 hello /a
GET /b -> 200 hello /b early
POST /upload -> 200 got 3
`
	if string(got) != want {
		t.Errorf("fetch client got:\n%s\nwant:\n%s", got, want)
		t.Logf("server stderr:\n%s", readServerStderr(t, cmd))
	}
}

func (w *serveH3World) driveFetch(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	clientSrc := filepath.Join(w.dir, "fetch_h3_client.fern")
	if err := os.WriteFile(clientSrc, []byte(fetchH3ClientSource), 0o644); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(w.dir, "fetch_h3_client.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("client build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runX86Bin(w.qemu, client, port, w.ca).CombinedOutput()
	if err != nil {
		t.Fatalf("fetch client: %v\n%s", err, out)
	}
	want := fmt.Sprintf(`GET /hello -> 200 hello /hello alt-svc=h3=":%s"; ma=86400
offered h3 port %s
GET /hello -> 200 hello /hello
POST /upload -> 200 got 100000
h3 kept: true
`, port, port)
	if string(out) != want {
		t.Errorf("fetch client got:\n%s\nwant:\n%s", out, want)
	}
	if strings.Contains(string(out), "error") {
		t.Logf("server stderr:\n%s", readServerStderr(t, cmd))
	}
}

func readServerStderr(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	if f, ok := cmd.Stderr.(*os.File); ok {
		b, err := os.ReadFile(f.Name())
		if err == nil {
			return string(b)
		}
	}
	return ""
}

// The client: a fetch pool over real sockets, two GETs and a POST to the
// served origin, the pool's view of the origin's HTTP/3 offer between them.
const fetchH3ClientSource = `import "std/fetch";
import "std/http";
import "std/tls/verify";
import "std/clock";
import "std/string";
import "std/utf8";
import "std/time";
import "std/i32";
import "std/option";
import "std/result";

function policy(roots: verify.Roots): fetch.Policy {
  return fetch.Policy { public_only: false, proxies: fetch.proxies("", "", ""), roots: Some(roots), jar: None, allow: None };
}

function shown(label: string, r: Result[HttpResponse, fetch.FetchError], with_alt: boolean): string {
  match (r) {
    Ok(resp) => {
      let line = label + " -> " + resp.status.to_string() + " " + utf8.from_bytes_lossy(resp.body_bytes());
      if (with_alt) {
        line = line + " alt-svc=" + resp.headers.get("alt-svc").unwrap_or("-");
      }
      return line;
    },
    Err(e) => {
      return label + " -> error " + e.message();
    }
  }
  return label;
}

function kept_h3(pool: fetch.Idle): i32 {
  let n = 0;
  for k in pool.kept.get() {
    if let Some(_) = k.h3 {
      n += 1;
    }
  }
  return n;
}

function main(): i32 {
  let argv = args();
  let port = argv[1];
  let Ok(roots) = verify.roots_from_file(argv[2]) else {
    return 2;
  };
  let pool = fetch.idle();
  let tr = fetch.sockets_with(pool);
  let base = "https://localhost:" + port;
  let p = policy(roots);
  // The first connection waits while an interpreted server starts.
  let bounds = fetch.Timeouts { ...fetch.timeouts(), connect: time.duration_seconds(60 as i64), total: time.duration_seconds(60 as i64) };
  print(shown("GET /hello", fetch.send_on(tr, fetch.get(base + "/hello").with_timeouts(bounds), p), true));
  print("offered h3 port " + pool.h3_offered("localhost", i32.parse_i32(port).unwrap_or(0), clock.instant()).to_string());
  print(shown("GET /hello", fetch.send_on(tr, fetch.get(base + "/hello").with_timeouts(bounds), p), false));
  print(shown("POST /upload", fetch.send_on(tr, fetch.request("POST", base + "/upload").with_bytes(__alloc_u8(100000)).with_timeouts(bounds), p), false));
  print("h3 kept: " + (kept_h3(pool) > 0).to_string());
  fetch.close_idle(tr);
  return 0;
}
`

// The client: a pool that keeps the origin's Alt-Svc offer and its session
// tickets but no connection, so each request after the first opens a new
// QUIC connection.
const fetchH3EarlySource = `import "std/fetch";
import "std/http";
import "std/tls/verify";
import "std/string";
import "std/utf8";
import "std/time";
import "std/option";
import "std/result";

function shown(label: string, r: Result[HttpResponse, fetch.FetchError]): string {
  match (r) {
    Ok(resp) => {
      return label + " -> " + resp.status.to_string() + " " + utf8.from_bytes_lossy(resp.body_bytes());
    },
    Err(e) => {
      return label + " -> error " + e.message();
    }
  }
  return label;
}

function main(): i32 {
  let argv = args();
  let Ok(roots) = verify.roots_from_file(argv[2]) else {
    return 2;
  };
  let pool = fetch.idle_limited(0, time.duration_seconds(90 as i64));
  let tr = fetch.sockets_with(pool);
  let base = "https://localhost:" + argv[1];
  let p = fetch.Policy { public_only: false, proxies: fetch.proxies("", "", ""), roots: Some(roots), jar: None, allow: None };
  let bounds = fetch.Timeouts { ...fetch.timeouts(), total: time.duration_seconds(60 as i64) };
  print(shown("GET /hello", fetch.send_on(tr, fetch.get(base + "/hello").with_timeouts(bounds), p)));
  print(shown("GET /a", fetch.send_on(tr, fetch.get(base + "/a").with_timeouts(bounds), p)));
  print(shown("GET /b", fetch.send_on(tr, fetch.get(base + "/b").with_timeouts(bounds), p)));
  print(shown("POST /upload", fetch.send_on(tr, fetch.request("POST", base + "/upload").with_bytes("x=1".bytes()).with_timeouts(bounds), p)));
  fetch.close_idle(tr);
  return 0;
}
`
