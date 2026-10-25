package e2e

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTLSServerAgainstGo(t *testing.T) {
	bin := buildFernCLI(t)
	for _, kind := range []string{"p256", "p384", "ed25519"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			w := newTLSServerWorld(t, kind)
			for _, be := range nativeBackends() {
				be := be
				t.Run(be.target, func(t *testing.T) {
					qemu := be.qemu(t)
					out := filepath.Join(w.dir, be.target+"_tls_server.bin")
					if o, err := exec.Command(bin, "-target", be.target, "-o", out, w.src).CombinedOutput(); err != nil {
						t.Fatalf("build failed: %v\n%s", err, o)
					}
					w.drive(t, be.run(qemu, out))
					w.driveClientAuth(t, be.run(qemu, out, "client-auth"))
				})
			}
		})
	}
}

func TestTLSServerAgainstGoInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for _, kind := range []string{"p256", "p384", "ed25519"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			w := newTLSServerWorld(t, kind)
			w.drive(t, exec.Command(bin, "-interp", w.src))
			w.driveClientAuth(t, exec.Command(bin, "-interp", w.src, "--", "client-auth"))
		})
	}
}

type tlsServerWorld struct {
	dir, src            string
	kind                string
	pool                *x509.CertPool
	other               *x509.CertPool
	cache               tls.ClientSessionCache
	client, otherClient tls.Certificate
}

func newTLSServerWorld(t *testing.T, kind string) *tlsServerWorld {
	t.Helper()
	w := &tlsServerWorld{dir: t.TempDir(), kind: kind, cache: tls.NewLRUClientSessionCache(4)}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern TLS Server Interop CA")
	other := newTestCA(t, mustKey(t, "p256"), "Fern TLS Server Interop Other CA")
	w.pool = x509.NewCertPool()
	w.pool.AddCert(ca.cert)
	w.other = x509.NewCertPool()
	w.other.AddCert(other.cert)
	key := mustKey(t, kind)
	leaf := ca.leaf(t, key, []string{"localhost"}, true)
	w.client = ca.clientLeaf(t, mustKey(t, "ed25519"), "fern-go-client")
	w.otherClient = other.clientLeaf(t, mustKey(t, "p256"), "fern-go-other")
	if err := os.WriteFile(filepath.Join(w.dir, "ca.pem"), ca.pem, 0o644); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(w.dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(w.dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM(t, key, kind == "p256"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.src = filepath.Join(w.dir, "tls_server.fern")
	src := strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath, "@CA@", filepath.Join(w.dir, "ca.pem"), "@N@", strconv.Itoa(len(w.cases())), "@M@", strconv.Itoa(len(w.clientAuthCases()))).Replace(tlsServerSource)
	if err := os.WriteFile(w.src, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func keyPEM(t *testing.T, key crypto.Signer, sec1 bool) []byte {
	t.Helper()
	if sec1 {
		der, err := x509.MarshalECPrivateKey(key.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

const tlsServerSource = `import "std/i32";
import "std/net";
import "std/string";
import "std/time";
import "std/tls/identity";
import "std/tls/server";
import "std/tls/verify";

function identity_of(): Result[identity.Identity, string] {
  let cert: string = "";
  let key: string = "";
  match (read_file("@CERT@")) {
    Ok(text) => {
      cert = text;
    },
    Err(_) => {
      return Err("cannot read the certificate");
    }
  }
  match (read_file("@KEY@")) {
    Ok(text) => {
      key = text;
    },
    Err(_) => {
      return Err("cannot read the key");
    }
  }
  match (identity.from_pem(cert, key)) {
    Ok(id) => {
      return Ok(id);
    },
    Err(e) => {
      return Err(e.message());
    }
  }
  return Err("identity");
}

// serve_one answers one connection: the line it sends, echoed, then the
// client's close. The report names the SNI, the protocol, the line and
// how the client ended.
function serve_one(fd: i32, cfg: server.Config): string {
  match (server.accept(fd, cfg, time.duration_seconds(60 as i64))) {
    Ok(c) => {
      return echo(c);
    },
    Err(e) => {
      return "refused " + e.message();
    }
  }
  return "";
}

function echo(c: server.Connection): string {
  let head: string = "[" + c.session.server_name() + "] [" + c.session.alpn() + "]";
  match (c.recv(time.duration_seconds(10 as i64))) {
    Ok(got) => {
      let line: string = string_from_bytes_unchecked(got.data).trim();
      match (got.conn.send(("echo " + line + "\n").bytes())) {
        Ok(sent) => {
          match (sent.recv(time.duration_seconds(10 as i64))) {
            Ok(end) => {
              let how: string = "open";
              if (end.ended) {
                how = "closed";
              }
              let mode: string = "fresh";
              if (c.session.resumed()) {
                mode = "resumed";
              }
              if (c.session.client_chain().len() > 0) {
                mode = mode + " client " + c.session.client_chain().len().to_string();
              }
              end.conn.close();
              return head + " " + line + " " + how + " " + mode;
            },
            Err(e) => {
              return head + " " + line + " recv " + e.message();
            }
          }
        },
        Err(e) => {
          return head + " send " + e.message();
        }
      }
    },
    Err(e) => {
      return head + " recv " + e.message();
    }
  }
  return "";
}

function main(): i32 {
  let cfg: server.Config = server.config(identity.Identity { chain: [], key: identity.Ed25519Seed([]) }, random_bytes(32));
  match (identity_of()) {
    Ok(id) => {
      cfg = server.config(id, random_bytes(32)).with_alpn(["fern-echo", "h2"]);
    },
    Err(e) => {
      print("identity: " + e);
      return 2;
    }
  }
  let count: i32 = @N@;
  let argv: string[] = args();
  if (argv.len() > 0 && argv[argv.len() - 1] == "client-auth") {
    count = @M@;
    match (verify.roots_from_file("@CA@")) {
      Ok(r) => {
        cfg = cfg.with_client_roots(r);
      },
      Err(e) => {
        print("roots: " + e.message());
        return 5;
      }
    }
  }
  let l: i32 = 0;
  match (net.listen_at(net.socket_addr(net.ipv4_loopback(), 0), net.listen_options())) {
    Ok(fd) => {
      l = fd;
    },
    Err(e) => {
      print("listen: " + e.message());
      return 3;
    }
  }
  match (net.local_port(l)) {
    Ok(port) => {
      print("port " + port.to_string());
    },
    Err(e) => {
      print("port: " + e.message());
      return 4;
    }
  }
  let n: i32 = 0;
  while (n < count) {
    n = n + 1;
    match (net.accept(l)) {
      Ok(fd) => {
        print("conn " + n.to_string() + ": " + serve_one(fd, cfg));
      },
      Err(e) => {
        print("conn " + n.to_string() + ": accept " + e.message());
      }
    }
  }
  tcp_close(l);
  return 0;
}
`

type tlsServerCase struct {
	name          string
	config        *tls.Config
	want, wantErr string
	resumed       bool
}

func (w *tlsServerWorld) cases() []tlsServerCase {
	trusted := func(mod func(*tls.Config)) *tls.Config {
		c := &tls.Config{RootCAs: w.pool, ServerName: "localhost", NextProtos: []string{"fern-echo", "h2"}, MinVersion: tls.VersionTLS13}
		mod(c)
		return c
	}
	cache := func(c *tls.Config) { c.ClientSessionCache = w.cache }
	return []tlsServerCase{
		{"default", trusted(cache), "[localhost] [fern-echo] default closed fresh", "", false},
		{"resume", trusted(cache), "[localhost] [fern-echo] resume closed resumed", "", true},
		{"retry", trusted(func(c *tls.Config) { c.CurvePreferences = []tls.CurveID{tls.CurveP256, tls.X25519} }), "[localhost] [fern-echo] retry closed fresh", "", false},
		{"address", trusted(func(c *tls.Config) { c.ServerName = "127.0.0.1"; c.NextProtos = nil }), "[] [] address closed fresh", "", false},
		{"untrusted", trusted(func(c *tls.Config) { c.RootCAs = w.other }), "refused tls handshake: the peer sent alert 42", "certificate signed by unknown authority", false},
		{"tls12", trusted(func(c *tls.Config) { c.MinVersion = tls.VersionTLS12; c.MaxVersion = tls.VersionTLS12 }), "refused tls handshake: the peer does not speak TLS 1.3", "protocol version not supported", false},
	}
}

func (w *tlsServerWorld) clientAuthCases() []tlsServerCase {
	cache := tls.NewLRUClientSessionCache(4)
	with := func(certs []tls.Certificate, cached bool) *tls.Config {
		c := &tls.Config{RootCAs: w.pool, ServerName: "localhost", NextProtos: []string{"fern-echo"}, MinVersion: tls.VersionTLS13, Certificates: certs}
		if cached {
			c.ClientSessionCache = cache
		}
		return c
	}
	return []tlsServerCase{
		{"mtls", with([]tls.Certificate{w.client}, true), "[localhost] [fern-echo] mtls closed fresh client 1", "", false},
		{"nocert", with(nil, false), "refused the peer sent no certificate", "certificate required", false},
		{"othercert", with([]tls.Certificate{w.otherClient}, false), "refused the peer's certificate is not trusted: tls verify: no path from the certificate reaches a trusted root", "bad certificate", false},
		{"mtls-resume", with([]tls.Certificate{w.client}, true), "[localhost] [fern-echo] mtls-resume closed resumed", "", true},
	}
}

func (w *tlsServerWorld) driveClientAuth(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	w.run(t, cmd, w.clientAuthCases())
}

func (w *tlsServerWorld) drive(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	w.run(t, cmd, w.cases())
}

func (w *tlsServerWorld) run(t *testing.T, cmd *exec.Cmd, cases []tlsServerCase) {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	r := bufio.NewReader(stdout)
	first, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, "port ") {
		rest, _ := io.ReadAll(r)
		t.Fatalf("server did not announce its port: %q %v\n%s", first, err, rest)
	}
	port := strings.TrimSpace(strings.TrimPrefix(first, "port "))
	var want []string
	for i, c := range cases {
		want = append(want, fmt.Sprintf("conn %d: %s", i+1, c.want))
		w.connect(t, port, c)
	}
	rest, _ := io.ReadAll(r)
	if err := cmd.Wait(); err != nil {
		t.Errorf("server exit: %v", err)
	}
	if got := strings.TrimRight(string(rest), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("server report:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func (w *tlsServerWorld) connect(t *testing.T, port string, c tlsServerCase) {
	t.Helper()
	d := &net.Dialer{Timeout: 60 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", "127.0.0.1:"+port, c.config)
	if c.wantErr != "" {
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
			if _, err = io.WriteString(conn, c.name+"\n"); err == nil {
				_, err = bufio.NewReader(conn).ReadString('\n')
			}
			conn.Close()
		}
		if err == nil {
			t.Errorf("%s: connected, want an error containing %q", c.name, c.wantErr)
		} else if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q, want it to contain %q", c.name, err, c.wantErr)
		}
		return
	}
	if err != nil {
		t.Errorf("%s: dial: %v", c.name, err)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	st := conn.ConnectionState()
	if st.Version != tls.VersionTLS13 {
		t.Errorf("%s: version %x, want TLS 1.3", c.name, st.Version)
	}
	wantCurve := tls.X25519MLKEM768
	if c.name == "retry" {
		wantCurve = tls.X25519
	}
	if st.CurveID != wantCurve {
		t.Errorf("%s: key exchange %v, want %v", c.name, st.CurveID, wantCurve)
	}
	if st.DidResume != c.resumed {
		t.Errorf("%s: resumed %v, want %v", c.name, st.DidResume, c.resumed)
	}
	if want := 1; !c.resumed && len(st.PeerCertificates) != want {
		t.Errorf("%s: %d peer certificates, want %d", c.name, len(st.PeerCertificates), want)
	}
	if _, err := io.WriteString(conn, c.name+"\n"); err != nil {
		t.Errorf("%s: write: %v", c.name, err)
		return
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "echo "+c.name+"\n" {
		t.Errorf("%s: read %q, %v; want %q", c.name, line, err, "echo "+c.name+"\n")
	}
}
