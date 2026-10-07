package e2e

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// std/tls/client and std/fetch's https against Go's crypto/tls: a server
// per leaf key type (ECDSA P-256, RSA, Ed25519) under a P-384 CA, one that
// takes only X25519 so the client's X25519MLKEM768 share draws a
// HelloRetryRequest, one under a CA the client does not trust, a CONNECT
// proxy, and a line-echo server for the socket client. The client trusts
// the CA through SSL_CERT_FILE. Both native backends and the interpreter.
func TestTLSClientAgainstGo(t *testing.T) {
	bin := buildFernCLI(t)
	w := startTLSWorld(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "tls_client.fern")
	if err := os.WriteFile(srcPath, []byte(w.source()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, be := range nativeBackends() {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_tls_client.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			before := w.mainConns.Load()
			cmd := be.run(qemu, out)
			cmd.Env = w.env()
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, got)
			}
			w.check(t, string(got), w.mainConns.Load()-before)
		})
	}
}

func TestTLSClientAgainstGoInterp(t *testing.T) {
	w := startTLSWorld(t)
	p := filepath.Join(t.TempDir(), "tls_client.fern")
	if err := os.WriteFile(p, []byte(w.source()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-interp", p)
	cmd.Env = w.env()
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	w.check(t, string(got), w.mainConns.Load())
}

type tlsWorld struct {
	caFile                              string
	main, retry, rsa, ed, bad, echo, px int
	mainConns                           atomic.Int64
	mu                                  sync.Mutex
	tunnels                             []string
}

func startTLSWorld(t *testing.T) *tlsWorld {
	t.Helper()
	w := &tlsWorld{}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern TLS Interop CA")
	other := newTestCA(t, mustKey(t, "p256"), "Fern TLS Interop Other CA")
	w.caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(w.caFile, ca.pem, 0o644); err != nil {
		t.Fatal(err)
	}
	names := []string{"localhost", "tls.test"}
	ecLeaf := ca.leaf(t, mustKey(t, "p256"), names, true)
	w.main = serveTLS(t, ecLeaf, nil, func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			w.mainConns.Add(1)
		}
	})
	w.retry = serveTLS(t, ecLeaf, []tls.CurveID{tls.X25519}, nil)
	w.rsa = serveTLS(t, ca.leaf(t, mustKey(t, "rsa"), []string{"localhost"}, false), nil, nil)
	w.ed = serveTLS(t, ca.leaf(t, mustKey(t, "ed25519"), []string{"localhost"}, false), nil, nil)
	w.bad = serveTLS(t, other.leaf(t, mustKey(t, "p256"), names, true), nil, nil)
	w.echo = serveEcho(t, ecLeaf)
	w.px = w.serveProxy(t)
	return w
}

// env is the environment the client runs in: the test CA as its root store,
// and no proxy from the host's own environment.
func (w *tlsWorld) env() []string {
	var out []string
	for _, kv := range os.Environ() {
		name := strings.ToLower(strings.SplitN(kv, "=", 2)[0])
		if name == "http_proxy" || name == "https_proxy" || name == "no_proxy" || name == "ssl_cert_file" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "SSL_CERT_FILE="+w.caFile)
}

func (w *tlsWorld) source() string {
	return strings.NewReplacer(
		"@MAIN@", strconv.Itoa(w.main), "@RETRY@", strconv.Itoa(w.retry), "@RSA@", strconv.Itoa(w.rsa),
		"@ED@", strconv.Itoa(w.ed), "@BAD@", strconv.Itoa(w.bad), "@ECHO@", strconv.Itoa(w.echo), "@PROXY@", strconv.Itoa(w.px),
	).Replace(tlsClientSource)
}

const tlsClientSource = `import "std/fetch";
import "std/http";
import "std/string";
import "std/time";
import "std/tls/client";
import "std/tls/verify";

function show(label: string, r: Result[HttpResponse, fetch.FetchError]): void {
  match (r) {
    Ok(resp) => {
      print(label + ": " + resp.status.to_string() + " " + string_from_bytes_unchecked(resp.body_bytes()));
    },
    Err(e) => {
      print(label + ": error " + e.message());
    }
  }
}

function proxies(https_proxy: string): fetch.Policy {
  return fetch.Policy { public_only: false, proxies: fetch.ProxyEnv { http_proxy: "", https_proxy: https_proxy, no_proxy: "" } };
}

function socket(roots: verify.Roots): string {
  match (client.connect("localhost", @ECHO@, client.config("localhost", roots).with_alpn(["fern-echo"]), time.duration_seconds(10 as i64))) {
    Ok(c) => {
      match (c.send("ping\n".bytes())) {
        Ok(sent) => {
          match (sent.recv(time.duration_seconds(10 as i64))) {
            Ok(got) => {
              got.conn.close();
              return sent.session.alpn() + " " + string_from_bytes_unchecked(got.data).trim();
            },
            Err(e) => {
              return "recv " + e.message();
            }
          }
        },
        Err(e) => {
          return "send " + e.message();
        }
      }
    },
    Err(e) => {
      return e.message();
    }
  }
  return "";
}

function main(): i32 {
  let tr: fetch.Sockets = fetch.sockets();
  show("first", fetch.send_on(tr, fetch.get("https://localhost:@MAIN@/a"), proxies("")));
  show("again", fetch.send_on(tr, fetch.get("https://localhost:@MAIN@/b"), proxies("")));
  fetch.close_idle(tr);
  show("address", fetch.send(fetch.get("https://127.0.0.1:@MAIN@/c")));
  show("retry", fetch.send(fetch.get("https://localhost:@RETRY@/d")));
  show("rsa", fetch.send(fetch.get("https://localhost:@RSA@/e")));
  show("ed25519", fetch.send(fetch.get("https://localhost:@ED@/f")));
  show("untrusted", fetch.send(fetch.get("https://localhost:@BAD@/")));
  show("wrongname", fetch.send(fetch.get("https://127.0.0.1:@RSA@/")));
  show("tunnel", fetch.send_on(fetch.sockets(), fetch.get("https://tls.test:@MAIN@/g"), proxies("http://127.0.0.1:@PROXY@")));
  match (verify.system_roots()) {
    Some(roots) => {
      print("socket: " + socket(roots));
    },
    None => {
      print("socket: no roots");
    }
  }
  return 0;
}
`

func (w *tlsWorld) check(t *testing.T, got string, mainConns int64) {
	t.Helper()
	ok := func(path, curve string) string {
		return "200 " + path + " HTTP/1.1 http/1.1 " + curve
	}
	want := strings.Join([]string{
		"first: " + ok("/a", "X25519MLKEM768"),
		"again: " + ok("/b", "X25519MLKEM768"),
		"address: " + ok("/c", "X25519MLKEM768"),
		"retry: " + ok("/d", "X25519"),
		"rsa: " + ok("/e", "X25519MLKEM768"),
		"ed25519: " + ok("/f", "X25519MLKEM768"),
		"untrusted: error TLS: the server's certificate is not trusted: tls verify: no path from the certificate reaches a trusted root",
		"wrongname: error TLS: the server's certificate is not trusted: tls verify: the certificate is not valid for 127.0.0.1",
		"tunnel: " + ok("/g", "X25519MLKEM768"),
		"socket: fern-echo echo ping",
	}, "\n") + "\n"
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	// first and again share one connection; address and tunnel open their own.
	if mainConns != 3 {
		t.Errorf("the main server took %d connections, want 3", mainConns)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if want := []string{"CONNECT tls.test:" + strconv.Itoa(w.main) + " HTTP/1.1"}; strings.Join(w.tunnels, "|") != strings.Join(want, "|") {
		t.Errorf("tunnels asked for: %q, want %q", w.tunnels, want)
	}
	w.tunnels = nil
}

func serveTLS(t *testing.T, cert tls.Certificate, curves []tls.CurveID, state func(net.Conn, http.ConnState)) int {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(rw, "%s %s %s %s", r.URL.Path, r.Proto, r.TLS.NegotiatedProtocol, r.TLS.CurveID)
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, CurvePreferences: curves, NextProtos: []string{"http/1.1"}}
	s.Config.ConnState = state
	s.StartTLS()
	t.Cleanup(s.Close)
	return s.Listener.Addr().(*net.TCPAddr).Port
}

// serveEcho answers each line on a TLS connection with "echo " and the line.
func serveEcho(t *testing.T, cert tls.Certificate) int {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, NextProtos: []string{"fern-echo"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Minute))
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					_, _ = io.WriteString(c, "echo "+line)
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// serveProxy opens a tunnel for each CONNECT to the asked-for port on
// loopback, whatever the host, and records the request line.
func (w *tlsWorld) serveProxy(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go w.tunnel(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func (w *tlsWorld) tunnel(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	for {
		h, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if h == "\r\n" {
			break
		}
	}
	line = strings.TrimRight(line, "\r\n")
	w.mu.Lock()
	w.tunnels = append(w.tunnels, line)
	w.mu.Unlock()
	parts := strings.Split(line, " ")
	if len(parts) != 3 || parts[0] != "CONNECT" {
		_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	_, port, _ := net.SplitHostPort(parts[1])
	up, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer up.Close()
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	go func() { _, _ = io.Copy(up, r) }()
	_, _ = io.Copy(c, up)
}

type testCA struct {
	cert *x509.Certificate
	key  crypto.Signer
	pem  []byte
}

func mustKey(t *testing.T, kind string) crypto.Signer {
	t.Helper()
	var k crypto.Signer
	var err error
	switch kind {
	case "p256":
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "p384":
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa":
		k, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ed25519":
		_, k, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newTestCA(t *testing.T, key crypto.Signer, name string) *testCA {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// leaf is a server certificate for names, and for 127.0.0.1 when withIP.
func (ca *testCA) leaf(t *testing.T, key crypto.Signer, names []string, withIP bool) tls.Certificate {
	t.Helper()
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if withIP {
		tmpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
