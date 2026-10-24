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
  let s: Stream = req.body_stream();
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
  if (s.fault() != 0) {
    return http.text(s.fault(), "fault " + s.fault().to_string());
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
      let cfg: serve.Config = serve.config().with_tls(server.config(id));
      match (env("SERVE_STREAM")) {
        Some(_) => {
          cfg = serve.Config { ...cfg, stream_bodies: true };
        },
        None => {}
      }
      return serve.run(0, cfg, handle);
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
		trace := &httptrace.ClientTrace{
			GotConn:          func(i httptrace.GotConnInfo) { reused = i.Reused },
			TLSHandshakeDone: func(cs tls.ConnectionState, err error) { resumed = cs.DidResume },
		}
		get := func(path string) (*http.Response, []byte) {
			t.Helper()
			req, _ := http.NewRequest("GET", base+path, nil)
			resp, err := client.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
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
		got := make([]byte, 64)
		if n, err := plain.Read(got); err == nil && (n == 0 || got[0] != 0x15) {
			t.Fatalf("a plaintext request was answered with %q", got[:n])
		}
		plain.Close()
		tr.CloseIdleConnections()
		if _, body := get("/c"); string(body) != "hello /c" || reused || !resumed {
			t.Fatalf("/c: %q reused=%v resumed=%v", body, reused, resumed)
		}
	})
}
