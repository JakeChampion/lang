// std/serve over HTTP/2 (`Config.with_h2`): a prior-knowledge h2c client
// (curl) and Go's net/http client negotiating h2 by ALPN under TLS, each
// making several requests on one connection, with bodies each way, against
// the interpreter and the native x86-64 build. The handler is the one an
// HTTP/1.1 server runs; the loop hands a connection that opens with the
// HTTP/2 preface to std/http/h2. A handler that suspends on an upstream
// parks per stream: the connection's other streams are answered while it
// waits, and its answer comes on its stream when the upstream does.
package e2e

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const serveH2Source = `import "std/fetch";
import "std/headers";
import "std/http";
import "std/platform";
import "std/serve";
import "std/time";
import "std/tls/identity";
import "std/tls/server";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/echo") {
    return http.bytes(200, req.body_bytes());
  }
  if (req.path == "/pingpong") {
    let source: Option[BodySource] = req.body.source;
    let producer: ChunkProducer = ChunkProducer { next: (i: i32): Option[u8[]] => {
      match (source) {
        Some(src) => {
          match (src.next()) {
            Some(chunk) => {
              return Some("echo:".bytes().concat(chunk));
            },
            None => {
              return None;
            }
          }
        },
        None => {
          return None;
        }
      }
      return None;
    }, idle_ms: 0, filler: [] };
    return HttpResponse { status: 200, body: BodyChunks(producer), headers: headers.header_map_new().append("Content-Type", "application/grpc"), trailers: headers.header_map_new().append("Grpc-Status", "0"), background: None };
  }
  if (req.path == "/park") {
    let parked: fetch.Timeouts = fetch.Timeouts { ...fetch.timeouts(), inactivity: time.duration_seconds(300 as i64), total: time.duration_seconds(600 as i64) };
    match (plat.http(fetch.get("http://8.8.8.8/hold").with_timeouts(parked))) {
      Ok(resp) => {
        return http.ok("held " + resp.status.to_string());
      },
      Err(e) => {
        return http.ok("held err " + e.message());
      }
    }
  }
  let host: string = "";
  match (req.headers.get("host")) {
    Some(h) => {
      host = h;
    },
    None => {}
  }
  return http.ok("hello " + req.method + " " + req.path + " " + host);
}

function main(): i32 {
  let cfg: serve.Config = serve.Config { ...serve.config().with_h2(), stream_bodies: true };
  match (env("H2_CERT")) {
    Some(cert_path) => {
      let cert: string = "";
      let key: string = "";
      match (read_file(cert_path)) {
        Ok(t) => {
          cert = t;
        },
        Err(_) => {
          return 2;
        }
      }
      match (env("H2_KEY")) {
        Some(key_path) => {
          match (read_file(key_path)) {
            Ok(t) => {
              key = t;
            },
            Err(_) => {
              return 3;
            }
          }
        },
        None => {
          return 3;
        }
      }
      match (identity.from_pem(cert, key)) {
        Ok(id) => {
          cfg = cfg.with_tls(server.config(id));
        },
        Err(e) => {
          eprint(e.message());
          return 4;
        }
      }
    },
    None => {}
  }
  return serve.run(0, cfg, handle);
}
`

// serveH2StateSource is `run_with` under `with_h2()`: a request count
// threaded through the handler, which only the stateful loop can carry
// from one request to the next.
const serveH2StateSource = `import "std/http";
import "std/platform";
import "std/serve";
import "core/int";

function handle(n: i32, req: HttpRequest, plat: platform.Host): (i32, HttpResponse) {
  return (n + 1, http.ok(req.path + "=" + int.int_to_string(n + 1)));
}

function main(): i32 {
  return serve.run_with(0, serve.config().with_h2(), 0, handle);
}
`

type serveH2World struct {
	dir, src, stateSrc, cert, key string
	pool                          *x509.CertPool
}

func newServeH2World(t *testing.T) *serveH2World {
	t.Helper()
	w := &serveH2World{dir: t.TempDir()}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern Serve H2 CA")
	w.pool = x509.NewCertPool()
	w.pool.AddCert(ca.cert)
	leaf := ca.leaf(t, mustKey(t, "p256"), []string{"localhost"}, true)
	w.cert = filepath.Join(w.dir, "cert.pem")
	if err := os.WriteFile(w.cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	w.key = filepath.Join(w.dir, "key.pem")
	if err := os.WriteFile(w.key, keyPEM(t, leaf.PrivateKey.(crypto.Signer), false), 0o600); err != nil {
		t.Fatal(err)
	}
	w.src = filepath.Join(w.dir, "serve_h2.fern")
	if err := os.WriteFile(w.src, []byte(serveH2Source), 0o644); err != nil {
		t.Fatal(err)
	}
	w.stateSrc = filepath.Join(w.dir, "serve_h2_state.fern")
	if err := os.WriteFile(w.stateSrc, []byte(serveH2StateSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestServeH2Interp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	w := newServeH2World(t)
	w.driveH2C(t, exec.Command(bin, "-interp", w.src))
	w.driveTLS(t, exec.Command(bin, "-interp", w.src))
	w.driveState(t, exec.Command(bin, "-interp", w.stateSrc))
}

func TestServeH2X86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	w := newServeH2World(t)
	out := filepath.Join(w.dir, "serve_h2.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	w.driveH2C(t, runX86Bin(qemu, out))
	w.driveTLS(t, runX86Bin(qemu, out))
	w.driveParked(t, runX86Bin(qemu, out))
	w.driveDuplex(t, runX86Bin(qemu, out))
	stateOut := filepath.Join(w.dir, "serve_h2_state.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", stateOut, w.stateSrc).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	w.driveState(t, runX86Bin(qemu, stateOut))
}

// driveH2C sends prior-knowledge HTTP/2 with curl: two GETs and a POST on
// one connection, each answered over HTTP/2.
func (w *serveH2World) driveH2C(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Fatal("curl is not installed; the h2c leg needs it")
	}
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	base := "http://" + addr
	run := func(args ...string) string {
		t.Helper()
		curl := exec.Command("curl", append([]string{"-s", "--noproxy", "*", "--http2-prior-knowledge", "-w", "\n%{http_version} %{http_code}\n"}, args...)...)
		out, err := curl.CombinedOutput()
		if err != nil {
			stderr, _ := os.ReadFile(stderrPath)
			t.Fatalf("curl %v: %v\n%s\nserver stderr:\n%s", args, err, out, stderr)
		}
		return string(out)
	}
	// Two GETs in one curl share one connection.
	if got, want := run(base+"/one", base+"/two"), fmt.Sprintf("hello GET /one %s\n2 200\nhello GET /two %s\n2 200\n", addr, addr); got != want {
		t.Fatalf("curl over h2c:\n%s\nwant:\n%s", got, want)
	}
	if got, want := run("--data-binary", "payload bytes", base+"/echo"), "payload bytes\n2 200\n"; got != want {
		t.Fatalf("curl POST over h2c:\n%s\nwant:\n%s", got, want)
	}
}

// driveDuplex streams a request body in three parts over one TLS HTTP/2
// stream and reads each part echoed back before sending the next: the
// handler's producer reads the body as it arrives and answers as it reads
// (`stream_bodies`), so a request and its response flow at once, as a
// gRPC ping-pong does. The native build only: the interpreter hands a body
// over whole.
func (w *serveH2World) driveDuplex(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Env = append(cmd.Environ(), "H2_CERT="+w.cert, "H2_KEY="+w.key)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: w.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	pr, pw := io.Pipe()
	req, err := http.NewRequest("POST", "https://"+addr+"/pingpong", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := client.Do(req)
	if err != nil {
		stderr, _ := os.ReadFile(stderrPath)
		t.Fatalf("POST /pingpong: %v\nserver stderr:\n%s", err, stderr)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 || resp.StatusCode != 200 {
		t.Fatalf("POST /pingpong: HTTP/%d %d, want HTTP/2 200", resp.ProtoMajor, resp.StatusCode)
	}
	for i := 1; i <= 3; i++ {
		msg := fmt.Sprintf("ping %d", i)
		if _, err := io.WriteString(pw, msg); err != nil {
			t.Fatalf("write %q: %v", msg, err)
		}
		want := "echo:" + msg
		got := make([]byte, len(want))
		if _, err := io.ReadFull(resp.Body, got); err != nil {
			stderr, _ := os.ReadFile(stderrPath)
			t.Fatalf("reading the echo of %q: %v\nserver stderr:\n%s", msg, err, stderr)
		}
		if string(got) != want {
			t.Fatalf("echo of %q: %q", msg, got)
		}
	}
	pw.Close()
	rest, err := io.ReadAll(resp.Body)
	if err != nil || len(rest) != 0 {
		t.Fatalf("after the request body ended: %q, %v", rest, err)
	}
	if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("trailer grpc-status %q, want \"0\"; trailers %v", got, resp.Trailer)
	}
}

// driveState sends prior-knowledge HTTP/2 with curl to the stateful loop:
// three GETs on one connection, each answered with the count so far. curl
// is told to use no proxy: `driveParked` names the fetch upstream as the
// process's `http_proxy` for the rest of the test, and curl, unlike Go's
// client, would send loopback through it too.
func (w *serveH2World) driveState(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	base := "http://" + addr
	curl := exec.Command("curl", "-s", "--noproxy", "*", "--http2-prior-knowledge", "-w", "\n%{http_version} %{http_code}\n", base+"/a", base+"/a", base+"/b")
	out, err := curl.CombinedOutput()
	if err != nil {
		stderr, _ := os.ReadFile(stderrPath)
		t.Fatalf("curl: %v\n%s\nserver stderr:\n%s", err, out, stderr)
	}
	if got, want := string(out), "/a=1\n2 200\n/a=2\n2 200\n/b=3\n2 200\n"; got != want {
		t.Fatalf("curl over h2c to the stateful loop:\n%s\nwant:\n%s", got, want)
	}
}

// driveTLS negotiates h2 by ALPN with Go's client and runs eight requests
// at once on the one connection it opens, then an upload. The connection
// is opened by a first request on its own: with none open, Go's transport
// dials one per queued request, and eight handshakes at once outrun the
// read deadline under the interpreter.
func (w *serveH2World) driveTLS(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Env = append(cmd.Environ(), "H2_CERT="+w.cert, "H2_KEY="+w.key)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: w.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	base := "https://" + addr
	first, err := client.Get(base + "/first")
	if err != nil {
		stderr, _ := os.ReadFile(stderrPath)
		t.Fatalf("GET /first: %v\nserver stderr:\n%s", err, stderr)
	}
	io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.ProtoMajor != 2 {
		t.Fatalf("GET /first: HTTP/%d.%d, want HTTP/2", first.ProtoMajor, first.ProtoMinor)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("/n%d", i)
			resp, err := client.Get(base + path)
			if err != nil {
				errs <- fmt.Errorf("GET %s: %v", path, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.ProtoMajor != 2 {
				errs <- fmt.Errorf("GET %s: HTTP/%d.%d, want HTTP/2", path, resp.ProtoMajor, resp.ProtoMinor)
				return
			}
			if want := "hello GET " + path + " " + addr; string(body) != want {
				errs <- fmt.Errorf("GET %s: body %q, want %q", path, body, want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		stderr, _ := os.ReadFile(stderrPath)
		t.Fatalf("%v\nserver stderr:\n%s", err, stderr)
	}
	payload := bytes.Repeat([]byte("0123456789"), 5000)
	resp, err := client.Post(base+"/echo", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /echo: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 2 || !bytes.Equal(body, payload) {
		t.Fatalf("POST /echo over HTTP/%d: %d bytes back, want %d", resp.ProtoMajor, len(body), len(payload))
	}
	if !strings.Contains(resp.Header.Get("Content-Length"), fmt.Sprint(len(payload))) {
		t.Fatalf("content-length %q, want %d", resp.Header.Get("Content-Length"), len(payload))
	}
}

// driveParked parks four handlers on the upstream's /hold target over one
// HTTP/2 connection, has a fifth request on the same connection answered
// while they wait, then releases the upstream and reads the four answers
// back on their streams. The native build only: the interpreter runs a
// handler's wait as a blocking call (docs/NET-P3-SUSPENSION-PLAN.md, the
// interp fallback), so nothing parks there.
func (w *serveH2World) driveParked(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	up := e2eharness.StartFetchUpstream(t)
	e2eharness.SetFetchProxy(t, up)
	cmd.Env = append(cmd.Environ(), "H2_CERT="+w.cert, "H2_KEY="+w.key)
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: w.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	base := "https://" + addr
	first, err := client.Get(base + "/first")
	if err != nil {
		t.Fatalf("GET /first: %v", err)
	}
	io.Copy(io.Discard, first.Body)
	first.Body.Close()
	const parked = 4
	type answer struct {
		body string
		err  error
	}
	answers := make(chan answer, parked)
	for i := 0; i < parked; i++ {
		go func() {
			resp, err := client.Get(base + "/park")
			if err != nil {
				answers <- answer{err: err}
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.ProtoMajor != 2 {
				answers <- answer{err: fmt.Errorf("GET /park: HTTP/%d, want HTTP/2", resp.ProtoMajor)}
				return
			}
			answers <- answer{body: string(body)}
		}()
	}
	deadline := time.Now().Add(30 * time.Second)
	for up.Held() < parked {
		if time.Now().After(deadline) {
			stderr, _ := os.ReadFile(stderrPath)
			t.Fatalf("%d handlers parked on the upstream after 30s, want %d\nserver stderr:\n%s", up.Held(), parked, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The connection's other streams are served while the four wait.
	resp, err := client.Get(base + "/meanwhile")
	if err != nil {
		t.Fatalf("GET /meanwhile while four handlers are parked: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := "hello GET /meanwhile " + addr; resp.ProtoMajor != 2 || string(body) != want {
		t.Fatalf("GET /meanwhile over HTTP/%d: %q, want %q", resp.ProtoMajor, body, want)
	}
	select {
	case a := <-answers:
		t.Fatalf("a parked handler answered before the upstream released it: %q %v", a.body, a.err)
	default:
	}
	// A gRPC call with a 50 ms grpc-timeout parks on the same upstream and
	// is answered with DEADLINE_EXCEEDED when the deadline passes, as a
	// trailers-only response, the upstream still holding.
	timed, err := http.NewRequest("POST", base+"/park", nil)
	if err != nil {
		t.Fatal(err)
	}
	timed.Header.Set("Content-Type", "application/grpc")
	timed.Header.Set("Grpc-Timeout", "50m")
	started := time.Now()
	late, err := client.Do(timed)
	if err != nil {
		t.Fatalf("gRPC call with grpc-timeout: %v", err)
	}
	io.Copy(io.Discard, late.Body)
	late.Body.Close()
	if late.StatusCode != 200 || late.Trailer.Get("Grpc-Status") != "4" {
		t.Fatalf("gRPC call past its deadline: %d, trailers %v, want 200 with grpc-status 4", late.StatusCode, late.Trailer)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("the deadline took %v to be answered", took)
	}
	if up.Held() != parked+1 {
		t.Fatalf("%d handlers parked on the upstream, want %d", up.Held(), parked+1)
	}
	up.Release()
	for i := 0; i < parked; i++ {
		select {
		case a := <-answers:
			if a.err != nil || a.body != "held 200" {
				stderr, _ := os.ReadFile(stderrPath)
				t.Fatalf("parked handler %d: %q %v\nserver stderr:\n%s", i+1, a.body, a.err, stderr)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("parked handler %d never answered after the upstream released", i+1)
		}
	}
}
