package e2e

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/serve with `Config.with_h3` (docs/NET-P8-HTTP3-PLAN.md slice 7): the
// server answers HTTP/3 on a UDP socket beside its TCP listener, and names
// it in Alt-Svc on every TCP response. A client built on std/http/h3q
// sends ten requests at once on one connection: one answered at once, an
// upload, one whose handler parks, a chunked body with trailers, a chunked
// body larger than a producer may leave unacknowledged, and HEAD requests
// for a whole body and two chunk producers, plus 204/304 chunked responses. Go's
// net/http checks the Alt-Svc header over TCP. The server runs under the
// interpreter and natively on x86-64; the client is native. The
// interpreter seals and opens every packet in interpreted AES-GCM, so it
// gets a smaller upload and a stream under the producer's cap.
func TestServeH3Interp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	// Pure interpreted TLS can spend more than the production default's
	// 10-second receive window doing crypto before this positive protocol
	// check begins. Give this fixture a bounded 60 seconds; the native case
	// and deadline/slow-client gates keep the production defaults.
	w := newServeH3WorldWithConfig(t, "serve.Config { ...serve.config(), recv_deadline: time.duration_seconds(60 as i64) }")
	w.drive(t, exec.Command(bin, "-interp", w.src), 20000, 4)
}

func TestServeH3X86_64(t *testing.T) {
	w := newServeH3World(t)
	out := filepath.Join(w.dir, "serve_h3.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	w.drive(t, runX86Bin(w.qemu, out), 300000, 100)
}

type serveH3World struct {
	dir, src, ca, client, fern, qemu string
	pool                             *x509.CertPool
}

func newServeH3World(t *testing.T) *serveH3World {
	return newServeH3WorldWithConfig(t, "serve.config()")
}

func newServeH3WorldWithConfig(t *testing.T, config string) *serveH3World {
	t.Helper()
	w := &serveH3World{dir: t.TempDir(), fern: buildFernCLI(t), qemu: x86QemuOrEmpty(t)}
	ca := newTestCA(t, mustKey(t, "p384"), "Fern Serve H3 CA")
	w.pool = x509.NewCertPool()
	w.pool.AddCert(ca.cert)
	w.ca = filepath.Join(w.dir, "ca.pem")
	if err := os.WriteFile(w.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	leaf := ca.leaf(t, mustKey(t, "p256"), []string{"localhost"}, true)
	certPath := filepath.Join(w.dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(w.dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM(t, leaf.PrivateKey.(crypto.Signer), false), 0o600); err != nil {
		t.Fatal(err)
	}
	w.src = filepath.Join(w.dir, "serve_h3.fern")
	if err := os.WriteFile(w.src, []byte(strings.NewReplacer("@CERT@", certPath, "@KEY@", keyPath, "@CONFIG@", config).Replace(serveH3Source)), 0o644); err != nil {
		t.Fatal(err)
	}
	clientSrc := filepath.Join(w.dir, "h3_client.fern")
	if err := os.WriteFile(clientSrc, []byte(serveH3ClientSource), 0o644); err != nil {
		t.Fatal(err)
	}
	w.client = filepath.Join(w.dir, "h3_client.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", w.client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("client build failed: %v\n%s", err, o)
	}
	return w
}

const serveH3Source = `import "std/http";
import "std/headers";
import "std/platform";
import "std/option";
import "std/serve";
import "std/string";
import "std/time";
import "std/tls/identity";
import "std/tls/server";

function part(i: i32): Option[u8[]] {
  if (i < 3) {
    return Some(("part " + i.to_string() + ";").bytes());
  }
  return None;
}

function piece(n: i32, i: i32): Option[u8[]] {
  if (i >= n) {
    return None;
  }
  let out: u8[] = __alloc_u8(8192);
  let k: i32 = 0;
  while (k < 8192) {
    out = out.with(k, ((i + k) % 251) as u8);
    k = k + 1;
  }
  return Some(out);
}

function parts_trailers(): HeaderMap {
  return headers.header_map_new().append("x-parts", "3");
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/upload") {
    return http.ok("got " + req.body_bytes().len().to_string());
  }
  if (req.path == "/slow") {
    plat.sleep(time.duration_millis(30 as i64));
    return http.ok("slept");
  }
  if (req.path == "/no-content-chunks") {
    return http.chunks(204, part);
  }
  if (req.path == "/not-modified-chunks") {
    return http.chunks(304, part);
  }
  if (req.path == "/chunks") {
    return http.chunks_with_trailers(200, part, parts_trailers);
  }
  if (req.path.starts_with("/stream?n=")) {
    let n: i32 = req.path.drop(10).parse_int().unwrap_or(0);
    return http.chunks(200, (i: i32): Option[u8[]] => piece(n, i));
  }
  return http.ok("hello " + req.path);
}

function main(): i32 {
  let Ok(cert) = read_file("@CERT@") else {
    return 2;
  };
  let Ok(key) = read_file("@KEY@") else {
    return 3;
  };
  let Ok(id) = identity.from_pem(cert, key) else {
    return 4;
  };
  return serve.run(0, @CONFIG@.with_tls(server.config(id)).with_h3(), handle);
}
`

// The client: ten requests at once on one HTTP/3 connection, each answer
// printed in the order asked once all are in, then a close.
const serveH3ClientSource = `import "std/net";
import "std/quic/conn";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/headers";
import "std/async";
import "std/option";
import "std/string";

function now(): i64 {
  return monotonic_ns();
}

function request(method: string, path: string, body: u8[]): HttpRequest {
  return HttpRequest { method: method, path: path, body: Stream { data: body, pos: 0, source: None }, headers: headers.header_map_new().append("host", "localhost"), trailers: headers.header_map_new() };
}

function summary(method: string, resp: HttpResponse): string {
  let body: u8[] = resp.body_bytes();
  let out: string = resp.status.to_string() + " ";
  if (body.len() > 64 || method == "HEAD" || resp.status == 204 || resp.status == 304) {
    out = out + body.len().to_string() + " bytes";
  } else {
    out = out + string_from_bytes_unchecked(body);
  }
  if (method == "HEAD") {
    out = out + " content-length=" + resp.headers.get("content-length").unwrap_or("-");
  }
  let i: i32 = 0;
  while (i < resp.trailers.names.len()) {
    out = out + " | " + resp.trailers.names[i] + "=" + resp.trailers.values[i];
    i = i + 1;
  }
  return out;
}

struct Asked { reqs: HttpRequest[], ids: i64[], answers: string[], got: i32, sent: boolean }

function asked(c: h3q.Conn, a: Asked): (h3q.Conn, Asked) {
  let evs: h3q.Event[] = c.events;
  let cur: h3q.Conn = c.events_taken();
  let next: Asked = a;
  for e in evs {
    match (e) {
      h3q.Responded(id, resp) => {
        let k: i32 = 0;
        while (k < next.ids.len()) {
          if (next.ids[k] == id) {
            next = Asked { ...next, answers: next.answers.with(k, next.reqs[k].method + " " + next.reqs[k].path + " -> " + summary(next.reqs[k].method, resp)), got: next.got + 1 };
          }
          k = k + 1;
        }
      },
      h3q.StreamDropped(id, code) => {
        print("stream " + id.to_string() + " dropped " + code.to_string());
        next = Asked { ...next, got: next.got + 1 };
      },
      _ => {}
    }
  }
  if (next.sent || !cur.ready()) {
    return (cur, next);
  }
  for r in next.reqs {
    let Ok(sent) = cur.request(r, "https") else {
      return (cur, next);
    };
    cur = sent.0;
    next = Asked { ...next, ids: next.ids.append(sent.1) };
  }
  return (cur, Asked { ...next, sent: true });
}

function main(): i32 {
  let argv: string[] = args();
  let Some(peer) = net.socket_addr_parse("127.0.0.1:" + argv[1]) else {
    return 2;
  };
  let Ok(roots) = verify.roots_from_file(argv[2]) else {
    return 2;
  };
  let cfg: conn.Config = h3q.transport(conn.config([]).client_side("localhost", roots));
  let Ok(q) = conn.client(cfg, random_bytes(conn.client_entropy(cfg)), now()) else {
    return 2;
  };
  let Some(any) = net.socket_addr_parse("127.0.0.1:0") else {
    return 2;
  };
  let Ok(sock) = net.udp_socket(any) else {
    return 2;
  };
  let nb: Result[void, net.NetError] = net.set_nonblocking(sock, true);
  let Some(upload) = argv[3].parse_int() else {
    return 2;
  };
  let reqs: HttpRequest[] = [request("GET", "/hello", []), request("POST", "/upload", __alloc_u8(upload)), request("GET", "/slow", []), request("GET", "/chunks", []), request("GET", "/stream?n=" + argv[4], []), request("HEAD", "/hello", []), request("HEAD", "/chunks", []), request("HEAD", "/stream?n=2", []), request("GET", "/no-content-chunks", []), request("GET", "/not-modified-chunks", [])];
  let a: Asked = Asked { reqs: reqs, ids: [], answers: ["", "", "", "", "", "", "", "", "", ""], got: 0, sent: false };
  let c: h3q.Conn = h3q.client(q, h3.config()).with_max_body(4194304 as i64);
  let buf: u8[] = __alloc_u8(65536);
  let started: i64 = now();
  while (a.got < reqs.len() && !c.done() && now() - started < 60000000000) {
    let c2: h3q.Conn = c.transmit(now());
    let out: u8[][] = c2.datagrams();
    c2 = c2.sent();
    c = c2;
    for d in out {
      let r: Result[i32, net.NetError] = net.send_to(sock, d, peer);
    }
    let wait: i32 = 50;
    let woke: i32 = async.wait_any([sock, 1], wait);
    let going: boolean = true;
    while (going) {
      match (net.recv_from(sock, buf)) {
        Ok(got) => {
          c = c.receive(buf[0:got.0], now());
        },
        Err(_) => {
          going = false;
        }
      }
    }
    if (c.deadline() != 0 && now() >= c.deadline()) {
      c = c.tick(now());
    }
    let (c3, a2) = asked(c, a);
    c = c3;
    a = a2;
  }
  for line in a.answers {
    print(line);
  }
  let closed: h3q.Conn = c.close(now()).transmit(now());
  let out: u8[][] = closed.datagrams();
  closed = closed.sent();
  for d in out {
    let r: Result[i32, net.NetError] = net.send_to(sock, d, peer);
  }
  if (a.got == reqs.len()) {
    return 0;
  }
  return 1;
}
`

// serveH3Want is what the client prints for an upload of `upload` bytes
// and a stream of `chunks` 8192-byte chunks.
func serveH3Want(upload, chunks int) string {
	return fmt.Sprintf(`GET /hello -> 200 hello /hello
POST /upload -> 200 got %d
GET /slow -> 200 slept
GET /chunks -> 200 part 0;part 1;part 2; | x-parts=3
GET /stream?n=%d -> 200 %d bytes
HEAD /hello -> 200 0 bytes content-length=12
HEAD /chunks -> 200 0 bytes content-length=-
HEAD /stream?n=2 -> 200 0 bytes content-length=-
GET /no-content-chunks -> 204 0 bytes
GET /not-modified-chunks -> 304 0 bytes
`, upload, chunks, chunks*8192)
}

func (w *serveH3World) drive(t *testing.T, cmd *exec.Cmd, upload, chunks int) {
	t.Helper()
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: w.pool, ServerName: "localhost", MinVersion: tls.VersionTLS13}}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	resp, err := client.Get("https://" + addr + "/hello")
	if err != nil {
		t.Fatalf("TCP request: %v", err)
	}
	resp.Body.Close()
	if got, want := resp.Header.Get("Alt-Svc"), `h3=":`+port+`"; ma=86400`; got != want {
		t.Errorf("Alt-Svc = %q, want %q", got, want)
	}
	out, err := runX86Bin(w.qemu, w.client, port, w.ca, strconv.Itoa(upload), strconv.Itoa(chunks)).CombinedOutput()
	if err != nil {
		t.Fatalf("h3 client: %v\n%s", err, out)
	}
	if want := serveH3Want(upload, chunks); string(out) != want {
		t.Errorf("h3 client got:\n%s\nwant:\n%s", out, want)
	}
}
