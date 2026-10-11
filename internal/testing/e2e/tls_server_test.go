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

// std/tls/server against Go's crypto/tls client: a Fern line-echo server
// under a P-384 CA with a P-256 leaf (its key as SEC 1), a P-384 leaf and an
// Ed25519 leaf (PKCS #8), each taking a client with the default key shares,
// one that sends only a P-256 share and is asked for X25519 by
// HelloRetryRequest, one connecting by address with no SNI, one that does
// not trust the CA, and one offering only TLS 1.2. Both native backends and
// the interpreter.
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
		})
	}
}

type tlsServerWorld struct {
	dir, src string
	kind     string
	pool     *x509.CertPool
	other    *x509.CertPool
}

// newTLSServerWorld writes the server's certificate, key and source. The
// P-256 key goes out as SEC 1 and the others as PKCS #8, so both readers
// meet a key Go wrote.
func newTLSServerWorld(t *testing.T, kind string) *tlsServerWorld {
	t.Helper()
	w := &tlsServerWorld{dir: t.TempDir(), kind: kind}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern TLS Server Interop CA")
	other := newTestCA(t, mustKey(t, "p256"), "Fern TLS Server Interop Other CA")
	w.pool = x509.NewCertPool()
	w.pool.AddCert(ca.cert)
	w.other = x509.NewCertPool()
	w.other.AddCert(other.cert)
	key := mustKey(t, kind)
	leaf := ca.leaf(t, key, []string{"localhost"}, true)
	certPath := filepath.Join(w.dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(w.dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM(t, key, kind == "p256"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.src = filepath.Join(w.dir, "tls_server.fern")
	src := strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath, "@N@", strconv.Itoa(len(w.cases()))).Replace(tlsServerSource)
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

// The server: a listener on a port the host picks, printed first, then one
// connection at a time, each reported on a line of its own.
const tlsServerSource = `import "std/i32";
import "std/net";
import "std/string";
import "std/time";
import "std/tls/server";

function identity(): Result[server.Identity, string] {
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
  match (server.identity_from_pem(cert, key)) {
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
              end.conn.close();
              return head + " " + line + " " + how;
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
  let cfg: server.Config = server.config(server.Identity { chain: [], key: server.Ed25519Seed([]) });
  match (identity()) {
    Ok(id) => {
      cfg = server.config(id).with_alpn(["fern-echo", "h2"]);
    },
    Err(e) => {
      print("identity: " + e);
      return 2;
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
  while (n < @N@) {
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
	name   string
	config *tls.Config
	// want is the server's report for the connection; wantErr is what the
	// Go client's dial must fail with, empty when it must succeed.
	want, wantErr string
}

func (w *tlsServerWorld) cases() []tlsServerCase {
	trusted := func(mod func(*tls.Config)) *tls.Config {
		c := &tls.Config{RootCAs: w.pool, ServerName: "localhost", NextProtos: []string{"fern-echo", "h2"}, MinVersion: tls.VersionTLS13}
		mod(c)
		return c
	}
	return []tlsServerCase{
		{"default", trusted(func(*tls.Config) {}), "[localhost] [fern-echo] default closed", ""},
		{"retry", trusted(func(c *tls.Config) { c.CurvePreferences = []tls.CurveID{tls.CurveP256, tls.X25519} }), "[localhost] [fern-echo] retry closed", ""},
		{"address", trusted(func(c *tls.Config) { c.ServerName = "127.0.0.1"; c.NextProtos = nil }), "[] [] address closed", ""},
		{"untrusted", trusted(func(c *tls.Config) { c.RootCAs = w.other }), "refused tls handshake: the peer sent alert 42", "certificate signed by unknown authority"},
		{"tls12", trusted(func(c *tls.Config) { c.MinVersion = tls.VersionTLS12; c.MaxVersion = tls.VersionTLS12 }), "refused tls handshake: the peer does not speak TLS 1.3", "protocol version not supported"},
	}
}

// drive starts the server, runs every case against it in order and checks
// its report.
func (w *tlsServerWorld) drive(t *testing.T, cmd *exec.Cmd) {
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
	for i, c := range w.cases() {
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
	// The interpreter takes seconds over a handshake.
	d := &net.Dialer{Timeout: 60 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", "127.0.0.1:"+port, c.config)
	if c.wantErr != "" {
		if err == nil {
			conn.Close()
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
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
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
	if len(st.PeerCertificates) != 1 {
		t.Errorf("%s: %d peer certificates, want 1", c.name, len(st.PeerCertificates))
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
