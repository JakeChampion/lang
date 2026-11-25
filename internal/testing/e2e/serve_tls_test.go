package e2e

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestServeTLSAgainstGoInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	w := newServeTLSWorld(t)
	for _, stream := range []bool{false, true} {
		w.drive(t, exec.Command(bin, "-interp", w.src), stream)
	}
}

func TestServeTLSAgainstGoX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	w := newServeTLSWorld(t)
	out := filepath.Join(w.dir, "serve_tls.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	for _, stream := range []bool{false, true} {
		w.drive(t, runX86Bin(qemu, out), stream)
	}
}

type serveTLSWorld struct {
	dir, src, file string
	pool           *x509.CertPool
}

const serveTLSFileContent = "served from a file under TLS\nline two\n"

func newServeTLSWorld(t *testing.T) *serveTLSWorld {
	t.Helper()
	w := &serveTLSWorld{dir: t.TempDir()}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern Serve TLS CA")
	w.pool = x509.NewCertPool()
	w.pool.AddCert(ca.cert)
	leaf := ca.leaf(t, mustKey(t, "p256"), []string{"localhost"}, true)
	certPath := filepath.Join(w.dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(w.dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM(t, leaf.PrivateKey.(crypto.Signer), false), 0o600); err != nil {
		t.Fatal(err)
	}
	w.file = filepath.Join(w.dir, "served.txt")
	if err := os.WriteFile(w.file, []byte(serveTLSFileContent), 0o644); err != nil {
		t.Fatal(err)
	}
	w.src = filepath.Join(w.dir, "serve_tls.fern")
	src := strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath, "@FILE@", w.file).Replace(serveTLSSource)
	if err := os.WriteFile(w.src, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

const serveTLSSource = `import "std/http";
import "std/i32";
import "std/platform";
import "std/serve";
import "std/tls/identity";
import "std/tls/server";
import "std/time";

import "std/rand";
function big(n: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append((i % 251) as u8);
    i = i + 1;
  }
  return out;
}

function sum_body(req: HttpRequest): HttpResponse {
  let s: ByteStream = req.body_stream();
  let count: i32 = 0;
  let sum: i32 = 0;
  let going: boolean = true;
  while (going) {
    let (piece, next) = s.read_n(1024);
    s = next;
    if (piece.len() == 0) {
      going = false;
    }
    let i: i32 = 0;
    while (i < piece.len()) {
      sum = sum + piece[i] as i32;
      i = i + 1;
    }
    count = count + piece.len();
  }
  if let Some(why) = s.fault() {
    return http.EndedEarly(why).to_response();
  }
  return http.ok("sum " + sum.to_string() + " len " + count.to_string());
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/echo") {
    return http.bytes(200, req.body_bytes());
  }
  if (req.path == "/sum") {
    return sum_body(req);
  }
  if (req.path == "/big") {
    return http.bytes(200, big(200000));
  }
  if (req.path == "/file") {
    return http.file("@FILE@").with_content_type("text/plain");
  }
  return http.ok("hello " + req.path);
}

function main(): i32 {
  let cert: string = "";
  let key: string = "";
  match (read_file("@CERT@")) {
    Ok(t) => {
      cert = t;
    },
    Err(_) => {
      return 2;
    }
  }
  match (read_file("@KEY@")) {
    Ok(t) => {
      key = t;
    },
    Err(_) => {
      return 3;
    }
  }
  match (identity.from_pem(cert, key)) {
    Ok(id) => {
      let invalid = server.Config { ...server.config_without_tickets(id), tickets: true, ticket_key: __alloc_u8(31) };
      if (serve.config().with_tls(invalid).is_ok()) {
        return 6;
      }
      let Err(refusal) = serve.run(0, serve.Config { ...serve.config(), tls: Some(invalid) }, handle) else { return 7; };
      if let None = refusal.tls_error { return 8; }
      let Ok(tickets) = server.config(id, rand.bytes(32)) else { return 9; };
      let Ok(cfg) = serve.config().with_tls(tickets) else { return 10; };
      match (env("SERVE_STREAM")) {
        Some(_) => {
          cfg = serve.Config { ...cfg, stream_bodies: true };
        },
        None => {}
      }
      if let Some(ms) = env("RECV_DEADLINE_MS") {
        cfg = serve.Config { ...cfg, recv_deadline: time.duration_millis(i32.parse_i32_radix(ms, 10).unwrap_or(10000) as i64) };
      }
      return serve.exit_code(serve.run(0, cfg, handle));
    },
    Err(e) => {
      eprint(e.message());
      return 4;
    }
  }
  return 5;
}
`

func (w *serveTLSWorld) drive(t *testing.T, cmd *exec.Cmd, stream bool) {
	t.Helper()
	if stream {
		cmd.Env = append(cmd.Environ(), "SERVE_STREAM=1")
	}
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	cache := tls.NewLRUClientSessionCache(4)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: w.pool, ServerName: "localhost", ClientSessionCache: cache, MinVersion: tls.VersionTLS13}, ExpectContinueTimeout: 5 * time.Second}
	client := &http.Client{Transport: tr, Timeout: 90 * time.Second}
	base := "https://" + addr
	name := "whole"
	if stream {
		name = "streamed"
	}
	t.Run(name, func(t *testing.T) {
		var reused, resumed bool
		// How far the last GET went, for the message of a failure.
		var connected, shook, wrote, answered bool
		trace := &httptrace.ClientTrace{
			GotConn:              func(i httptrace.GotConnInfo) { reused, connected = i.Reused, true },
			TLSHandshakeDone:     func(cs tls.ConnectionState, err error) { resumed, shook = cs.DidResume, err == nil },
			WroteRequest:         func(i httptrace.WroteRequestInfo) { wrote = i.Err == nil },
			GotFirstResponseByte: func() { answered = true },
		}
		get := func(path string) (*http.Response, []byte) {
			t.Helper()
			connected, shook, wrote, answered = false, false, false, false
			began := time.Now()
			req, _ := http.NewRequest("GET", base+path, nil)
			resp, err := client.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
			if err != nil {
				t.Fatalf("GET %s: %v after %v (connection %v, reused %v, new TLS handshake done %v, request written %v, first response byte %v)",
					path, err, time.Since(began).Round(time.Millisecond), connected, connected && reused, shook, wrote, answered)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return resp, body
		}
		if resp, body := get("/a"); resp.StatusCode != 200 || string(body) != "hello /a" || resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("/a: %d %q tls=%v", resp.StatusCode, body, resp.TLS)
		}
		if _, body := get("/b"); string(body) != "hello /b" || !reused {
			t.Fatalf("/b: %q reused=%v", body, reused)
		}
		upload := bytes.Repeat([]byte("0123456789abcdef"), 6400)
		resp, err := client.Post(base+"/echo", "application/octet-stream", bytes.NewReader(upload))
		if err != nil {
			t.Fatalf("POST /echo: %v", err)
		}
		echoed, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Equal(echoed, upload) {
			t.Fatalf("/echo: %d bytes back, want %d", len(echoed), len(upload))
		}
		_, bigBody := get("/big")
		if len(bigBody) != 200000 {
			t.Fatalf("/big: %d bytes, want 200000", len(bigBody))
		}
		for i, b := range bigBody {
			if int(b) != i%251 {
				t.Fatalf("/big: byte %d is %d", i, b)
			}
		}
		if resp, body := get("/file"); string(body) != serveTLSFileContent || resp.Header.Get("Content-Type") != "text/plain" {
			t.Fatalf("/file: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
		}
		if stream {
			sum := 0
			for _, b := range upload {
				sum += int(b)
			}
			req, _ := http.NewRequest("POST", base+"/sum", bytes.NewReader(upload))
			req.Header.Set("Expect", "100-continue")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("POST /sum: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if want := fmt.Sprintf("sum %d len %d", sum, len(upload)); string(body) != want {
				t.Fatalf("/sum: %q, want %q", body, want)
			}
		}
		plain, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = plain.SetDeadline(time.Now().Add(30 * time.Second))
		fmt.Fprint(plain, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
		// A fatal unexpected_message alert. A close_notify here is the receive
		// deadline of the server, which waited for a record that does not come.
		got := make([]byte, 64)
		if n, err := plain.Read(got); err != nil || !bytes.Equal(got[:n], []byte{0x15, 3, 3, 0, 2, 2, 10}) {
			t.Fatalf("a plaintext request was answered with %x (%v), want a fatal unexpected_message alert", got[:n], err)
		}
		plain.Close()
		tr.CloseIdleConnections()
		if _, body := get("/c"); string(body) != "hello /c" || reused || !resumed {
			t.Fatalf("/c: %q reused=%v resumed=%v", body, reused, resumed)
		}
	})
}

// slowConn delays each write after the first, so a TLS client sends each of
// its later flights, and then its request, after a pause.
type slowConn struct {
	net.Conn
	writes int
	pause  time.Duration
}

func (c *slowConn) Write(b []byte) (int, error) {
	c.writes++
	if c.writes > 1 {
		time.Sleep(c.pause)
	}
	return c.Conn.Write(b)
}

// The receive deadline guards the time the server waits for the client. It
// restarts when the handshake moves on, so a client whose flights each come
// within the deadline is served, although the whole exchange takes longer.
func TestServeTLSDeadlineRestartsAfterHandshakeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	w := newServeTLSWorld(t)
	out := filepath.Join(w.dir, "serve_tls.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	cmd := runX86Bin(qemu, out)
	cmd.Env = append(cmd.Environ(), "RECV_DEADLINE_MS=2000")
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	raw, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
	// The client's Finished comes 1.5 s after the accept and its request
	// 3 s after it: past a deadline counted from the accept alone.
	conn := tls.Client(&slowConn{Conn: raw, pause: 1500 * time.Millisecond}, &tls.Config{RootCAs: w.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13})
	began := time.Now()
	if err := conn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if _, err := fmt.Fprint(conn, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	reply, err := io.ReadAll(conn)
	if !bytes.Contains(reply, []byte("hello /slow")) {
		t.Fatalf("after %v: reply %q (%v), want hello /slow", time.Since(began).Round(time.Millisecond), reply, err)
	}
	if took := time.Since(began); took < 2500*time.Millisecond {
		t.Fatalf("the exchange took %v; the pauses did not reach past the first deadline", took)
	}
}
