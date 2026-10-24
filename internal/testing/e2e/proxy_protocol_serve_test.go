package e2e

import (
	"bufio"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestProxyProtocolServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	w := newProxyWorld(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", w.src))
	checkProxyPlain(t, addr)
	cmd := exec.Command(bin, "-interp", w.src)
	cmd.Env = append(os.Environ(), "PROXY_TLS=1")
	tlsAddr, _ := e2eharness.StartInheritedServer(t, cmd)
	checkProxyTLS(t, tlsAddr, w.pool)
}

func TestProxyProtocolServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	w := newProxyWorld(t)
	out := filepath.Join(filepath.Dir(w.src), "proxy_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkProxyPlain(t, addr)
	cmd := runX86Bin(qemu, out)
	cmd.Env = append(cmd.Environ(), "PROXY_TLS=1")
	tlsAddr, _ := e2eharness.StartInheritedServer(t, cmd)
	checkProxyTLS(t, tlsAddr, w.pool)
}

type proxyWorld struct {
	src  string
	pool *x509.CertPool
}

func newProxyWorld(t *testing.T) proxyWorld {
	t.Helper()
	dir := t.TempDir()
	ca := newTestCA(t, mustKey(t, "p384"), "Fern PROXY CA")
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	leaf := ca.leaf(t, mustKey(t, "p256"), []string{"localhost"}, true)
	certPath := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM(t, leaf.PrivateKey.(crypto.Signer), false), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "proxy_serve.fern")
	text := strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath).Replace(proxyServeSource)
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return proxyWorld{src: src, pool: pool}
}

const proxyServeSource = `import "std/http";
import "std/platform";
import "std/serve";
import "std/time";
import "std/tls/identity";
import "std/tls/server";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  match (plat.peer()) {
    Some(p) => {
      return http.ok(p.ip.to_string());
    },
    None => {
      return http.ok("-");
    }
  }
  return http.ok("-");
}

function with_tls(cfg: serve.Config): serve.Config {
  match (read_file("@CERT@")) {
    Ok(cert) => {
      match (read_file("@KEY@")) {
        Ok(key) => {
          match (identity.from_pem(cert, key)) {
            Ok(id) => {
              return cfg.with_tls(server.config(id));
            },
            Err(e) => {}
          }
        },
        Err(e) => {}
      }
    },
    Err(e) => {}
  }
  return cfg;
}

function main(): i32 {
  // The interpreted TLS handshake can outlast the default 10 s read deadline.
  let cfg: serve.Config = serve.Config { ...serve.config(), proxy_protocol: true, recv_deadline: time.duration_seconds(60 as i64) };
  match (env("PROXY_TLS")) {
    Some(_) => {
      cfg = with_tls(cfg);
    },
    None => {}
  }
  return serve.run(0, cfg, handle);
}
`

var proxyV2Signature = []byte{13, 10, 13, 10, 0, 13, 10, 'Q', 'U', 'I', 'T', 10}

func proxyV2(cmd byte, family byte, payload []byte) []byte {
	out := append([]byte{}, proxyV2Signature...)
	out = append(out, 0x20|cmd, family, byte(len(payload)>>8), byte(len(payload)))
	return append(out, payload...)
}

const proxyRequest = "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"

func proxyExchange(t *testing.T, addr string, parts ...[]byte) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	for i, p := range parts {
		if i > 0 {
			time.Sleep(50 * time.Millisecond)
		}
		if _, err := conn.Write(p); err != nil {
			return ""
		}
	}
	return readProxyAnswer(t, conn)
}

func readProxyAnswer(t *testing.T, conn net.Conn) string {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Logf("no response: %v", err)
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func checkProxyPlain(t *testing.T, addr string) {
	t.Helper()
	v6 := append(net.ParseIP("2001:db8::7").To16(), net.ParseIP("2001:db8::1").To16()...)
	v6 = append(v6, 0x15, 0xb3, 0x01, 0xbb)
	cases := []struct {
		name  string
		parts [][]byte
		want  string
	}{
		{"v1", [][]byte{[]byte("PROXY TCP4 203.0.113.7 192.0.2.1 5555 80\r\n" + proxyRequest)}, "203.0.113.7"},
		{"v2 IPv6", [][]byte{append(proxyV2(1, 0x21, v6), proxyRequest...)}, "2001:db8::7"},
		{"split header", [][]byte{[]byte("PROXY TCP4 198.51."), []byte("100.9 192.0.2.1 1 80\r\n" + proxyRequest)}, "198.51.100.9"},
		{"v2 LOCAL", [][]byte{append(proxyV2(0, 0x00, nil), proxyRequest...)}, "127.0.0.1"},
		{"no header", [][]byte{[]byte(proxyRequest)}, ""},
	}
	for _, c := range cases {
		if got := proxyExchange(t, addr, c.parts...); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func checkProxyTLS(t *testing.T, addr string, pool *x509.CertPool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := conn.Write([]byte("PROXY TCP4 203.0.113.8 192.0.2.1 5555 443\r\n")); err != nil {
		t.Fatal(err)
	}
	tc := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("TLS behind PROXY: %v", err)
	}
	tc.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := tc.Write([]byte(proxyRequest)); err != nil {
		t.Fatal(err)
	}
	if got := readProxyAnswer(t, tc); got != "203.0.113.8" {
		t.Errorf("TLS behind PROXY: %q, want %q", got, "203.0.113.8")
	}
}
