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
// the CA through SSL_CERT_FILE, and on one held transport through the
// policy's own roots, then an empty store the kept connection must not
// satisfy, then its own roots again. Both native backends and the
// interpreter.
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
	caFile, clientCert, clientKey       string
	main, retry, rsa, ed, bad, echo, px int
	mtls, h2                            int
	mainConns, h2Conns                  atomic.Int64
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
	w.h2 = serveTLSH2(t, ecLeaf, func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			w.h2Conns.Add(1)
		}
	})
	w.rsa = serveTLS(t, ca.leaf(t, mustKey(t, "rsa"), []string{"localhost"}, false), nil, nil)
	w.ed = serveTLS(t, ca.leaf(t, mustKey(t, "ed25519"), []string{"localhost"}, false), nil, nil)
	w.bad = serveTLS(t, other.leaf(t, mustKey(t, "p256"), names, true), nil, nil)
	w.echo = serveEcho(t, ecLeaf, nil)
	clientLeaf := ca.clientLeaf(t, mustKey(t, "ed25519"), "fern-client")
	w.clientCert = filepath.Join(t.TempDir(), "client.pem")
	if err := os.WriteFile(w.clientCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientLeaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	w.clientKey = filepath.Join(t.TempDir(), "client-key.pem")
	if err := os.WriteFile(w.clientKey, keyPEM(t, clientLeaf.PrivateKey.(crypto.Signer), false), 0o600); err != nil {
		t.Fatal(err)
	}
	clients := x509.NewCertPool()
	clients.AddCert(ca.cert)
	w.mtls = serveEcho(t, ecLeaf, clients)
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
		"@MTLS@", strconv.Itoa(w.mtls), "@CLIENT_CERT@", w.clientCert, "@CLIENT_KEY@", w.clientKey, "@H2@", strconv.Itoa(w.h2),
	).Replace(tlsClientSource)
}

const tlsClientSource = `import "std/fetch";
import "std/http";
import "std/string";
import "std/time";
import "std/tls";
import "std/tls/client";
import "std/tls/identity";
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
  return fetch.Policy { public_only: false, proxies: fetch.proxies("", https_proxy, ""), roots: None, jar: None, allow: None };
}

// trusting is a policy with its own roots in place of the machine's.
function trusting(roots: verify.Roots): fetch.Policy {
  return fetch.Policy { ...proxies(""), roots: Some(roots) };
}

// socket runs one line through the echo server, then another on a session
// resumed from the ticket the first was issued.
function socket(roots: verify.Roots): string {
  let first: client.Config = client.config("localhost", roots).with_alpn(["fern-echo"]);
  match (connected(first)) {
    Ok(c) => {
      match (c.send("ping\n".bytes())) {
        Ok(sent) => {
          match (sent.recv(time.duration_seconds(10 as i64))) {
            Ok(got) => {
              got.conn.close();
              let line: string = sent.session.alpn() + " " + string_from_bytes_unchecked(got.data).trim();
              return line + " / " + resumed_line(first, got.conn.tickets);
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

// connected is a handshake with the echo server.
function connected(cfg: client.Config): Result[client.Connection, tls.TlsError] {
  return client.connect("localhost", @ECHO@, cfg, time.duration_seconds(10 as i64));
}

// mtls echoes a line at the server requiring a client certificate, with
// the identity read from the files, and without one.
function mtls(roots: verify.Roots): string {
  let cfg: client.Config = client.config("localhost", roots).with_alpn(["fern-echo"]);
  let cert: string = "";
  let key: string = "";
  match (read_file("@CLIENT_CERT@")) {
    Ok(t) => {
      cert = t;
    },
    Err(_) => {
      return "no cert file";
    }
  }
  match (read_file("@CLIENT_KEY@")) {
    Ok(t) => {
      key = t;
    },
    Err(_) => {
      return "no key file";
    }
  }
  match (identity.from_pem(cert, key)) {
    Ok(id) => {
      return echoed(cfg.with_identity(id)) + " / " + echoed(cfg);
    },
    Err(e) => {
      return e.message();
    }
  }
  return "";
}

function echoed(cfg: client.Config): string {
  match (client.connect("localhost", @MTLS@, cfg, time.duration_seconds(10 as i64))) {
    Ok(c) => {
      match (c.send("hello\n".bytes())) {
        Ok(sent) => {
          match (sent.recv(time.duration_seconds(10 as i64))) {
            Ok(got) => {
              got.conn.close();
              return string_from_bytes_unchecked(got.data).trim();
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

// resumed_line echoes a line on a session resumed from the first ticket.
function resumed_line(cfg: client.Config, tickets: client.Ticket[]): string {
  if (tickets.len() == 0) {
    return "no ticket";
  }
  match (connected(cfg.with_ticket(tickets[0]))) {
    Ok(c) => {
      let mode: string = "fresh";
      if (c.session.resumed()) {
        mode = "resumed";
      }
      match (c.send("again\n".bytes())) {
        Ok(sent) => {
          match (sent.recv(time.duration_seconds(10 as i64))) {
            Ok(got) => {
              got.conn.close();
              return mode + " " + string_from_bytes_unchecked(got.data).trim();
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
  let over_h2: fetch.Sockets = fetch.sockets();
  show("h2", fetch.send_on(over_h2, fetch.get("https://localhost:@H2@/k").with_header("X-Fern", "yes"), proxies("")));
  show("h2 again", fetch.send_on(over_h2, fetch.get("https://localhost:@H2@/l").with_body(BodyText("body-bytes")), proxies("")));
  fetch.close_idle(over_h2);
  match (verify.system_roots()) {
    Some(roots) => {
      let held: fetch.Sockets = fetch.sockets();
      show("given", fetch.send_on(held, fetch.get("https://localhost:@RSA@/h"), trusting(roots)));
      show("none", fetch.send_on(held, fetch.get("https://localhost:@RSA@/i"), trusting(verify.roots([]))));
      show("given again", fetch.send_on(held, fetch.get("https://localhost:@RSA@/j"), trusting(roots)));
      fetch.close_idle(held);
      print("socket: " + socket(roots));
      print("mtls: " + mtls(roots));
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
		"untrusted: error TLS: the peer's certificate is not trusted: tls verify: no path from the certificate reaches a trusted root",
		"wrongname: error TLS: the peer's certificate is not trusted: tls verify: the certificate is not valid for 127.0.0.1",
		"tunnel: " + ok("/g", "X25519MLKEM768"),
		"h2: 200 /k HTTP/2.0 h2 body=0 x-fern=yes",
		"h2 again: 200 /l HTTP/2.0 h2 body=10 x-fern=-",
		"given: " + ok("/h", "X25519MLKEM768"),
		"none: error TLS: the peer's certificate is not trusted: tls verify: no path from the certificate reaches a trusted root",
		"given again: " + ok("/j", "X25519MLKEM768"),
		"socket: fern-echo echo ping / resumed resumed-echo again",
		"mtls: echo fern-client hello / recv tls handshake: the peer sent alert 116",
	}, "\n") + "\n"
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
	// first and again share one connection; address and tunnel open their own.
	if mainConns != 3 {
		t.Errorf("the main server took %d connections, want 3", mainConns)
	}
	// h2 and h2 again share one HTTP/2 connection.
	if n := w.h2Conns.Swap(0); n != 1 {
		t.Errorf("the HTTP/2 server took %d connections, want 1", n)
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

// serveTLSH2 is serveTLS offering h2 by ALPN, Go's HTTP/2 server behind
// it: the handler names the path, the protocol spoken, the ALPN protocol,
// the request body's length and the X-Fern header's value.
func serveTLSH2(t *testing.T, cert tls.Certificate, state func(net.Conn, http.ConnState)) int {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fern := r.Header.Get("X-Fern")
		if fern == "" {
			fern = "-"
		}
		fmt.Fprintf(rw, "%s %s %s body=%d x-fern=%s", r.URL.Path, r.Proto, r.TLS.NegotiatedProtocol, len(body), fern)
	}))
	s.EnableHTTP2 = true
	s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}
	s.Config.ConnState = state
	s.StartTLS()
	t.Cleanup(s.Close)
	return s.Listener.Addr().(*net.TCPAddr).Port
}

// serveEcho answers each line on a TLS connection with "echo " and the
// line, "resumed-echo " on a resumed session; with clients set it requires
// a client certificate under them and names its subject.
func serveEcho(t *testing.T, cert tls.Certificate, clients *x509.CertPool) int {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, NextProtos: []string{"fern-echo"}}
	if clients != nil {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clients
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
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
					st := c.(*tls.Conn).ConnectionState()
					echo := "echo "
					if st.DidResume {
						echo = "resumed-echo "
					}
					if len(st.PeerCertificates) > 0 {
						echo += st.PeerCertificates[0].Subject.CommonName + " "
					}
					_, _ = io.WriteString(c, echo+line)
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

// clientLeaf is a client certificate with the common name given.
func (ca *testCA) clientLeaf(t *testing.T, key crypto.Signer, name string) tls.Certificate {
	t.Helper()
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
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
