package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A client whose address changes mid-connection, as NAT rebinding changes
// it: the first request goes from one UDP socket, the second from another
// on a new port. The server validates the new path and answers there, and
// addresses the client by a spare connection ID, retiring the first.
func TestServeH3RebindX86_64(t *testing.T) {
	w := newServeH3World(t)
	out := filepath.Join(w.dir, "serve_h3.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	clientSrc := filepath.Join(w.dir, "h3_rebind_client.fern")
	if err := os.WriteFile(clientSrc, []byte(serveH3RebindClient), 0o644); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(w.dir, "h3_rebind_client.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("client build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(w.qemu, out))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runX86Bin(w.qemu, client, port, w.ca).CombinedOutput()
	if err != nil {
		t.Fatalf("rebinding client: %v\n%s", err, got)
	}
	want := "GET /hello -> 200 hello /hello\nGET /hello -> 200 hello /hello\nanswered on the second socket, first connection id retired: true true\n"
	if string(got) != want {
		t.Errorf("rebinding client got:\n%s\nwant:\n%s", got, want)
	}
}

// The client: one HTTP/3 connection, a GET from the first socket, then a GET
// from a second socket bound to another port, each answer read on the socket
// it was asked from.
const serveH3RebindClient = `import "std/time";
import "std/clock";
import "std/net";
import "std/quic/conn";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/headers";
import "std/hex";
import "std/async";
import "std/option";
import "std/string";
import "std/utf8";

import "std/rand";
struct Leg { c: h3q.Conn, sock: Socket, answer: string, dropped: boolean }

function has_id(ids: u8[][], id: u8[]): boolean {
  for k in ids {
    if (hex.encode(k) == hex.encode(id)) {
      return true;
    }
  }
  return false;
}

function request(): HttpRequest {
  return HttpRequest { method: "GET", path: "/hello", body: ByteStream { data: [], pos: 0, source: None }, headers: headers.new().append("host", "localhost"), trailers: headers.new() };
}

// asked_on sends a GET on the connection from the given socket and waits
// for its answer on that socket.
function asked_on(leg: Leg, peer: net.SocketAddr): Leg {
  let cur = leg;
  let buf: u8[] = __alloc_u8(65536);
  let sent = false;
  let started = clock.instant();
  while (cur.answer == "" && !cur.dropped && !cur.c.done() && clock.elapsed(started).cmp(time.duration_seconds(20)) < 0) {
    let now = clock.instant();
    if (!sent && cur.c.ready()) {
      let Ok(opened) = cur.c.request(request().outgoing("https")) else {
        return Leg { ...cur, dropped: true };
      };
      cur = Leg { ...cur, c: opened.0 };
      sent = true;
    }
    let next = cur.c.transmit(now);
    let out = next.datagrams();
    cur = Leg { ...cur, c: next.sent() };
    for d in out {
      let r = net.send_to(cur.sock, d, peer);
    }
    let woke = async.wait_any([cur.sock.fd, 1], Some(clock.instant().add(time.duration_millis(10))));
    let reading = true;
    while (reading) {
      match (net.recv_from(cur.sock, buf)) {
        Ok(d) => {
          cur = Leg { ...cur, c: cur.c.receive_from(buf[0:d.0], [1u8], clock.instant()) };
        },
        Err(_) => {
          reading = false;
        }
      }
    }
    if let Some(at) = cur.c.deadline() {
      if (clock.instant().cmp(at) >= 0) {
        cur = Leg { ...cur, c: cur.c.tick(clock.instant()) };
      }
    }
    let evs = cur.c.events;
    cur = Leg { ...cur, c: cur.c.events_taken() };
    for e in evs {
      match (e) {
        h3q.Responded(_, resp) => {
          cur = Leg { ...cur, answer: "GET /hello -> " + resp.status.to_string() + " " + utf8.from_bytes_lossy(resp.body_bytes()) };
        },
        h3q.StreamDropped(_, _) => {
          cur = Leg { ...cur, dropped: true };
        },
        _ => {}
      }
    }
  }
  return cur;
}

function main(): i32 {
  let argv = args();
  let Ok(peer) = net.socket_addr_parse("127.0.0.1:" + argv[1]) else {
    return 2;
  };
  let Ok(roots) = verify.roots_from_file(argv[2]) else {
    return 2;
  };
  let Ok(any) = net.socket_addr_parse("127.0.0.1:0") else {
    return 2;
  };
  let cfg = h3q.transport(conn.config([]).client_side("localhost", roots));
  let Ok(q) = conn.client(cfg, rand.bytes(conn.client_entropy(cfg)), clock.instant(), clock.now()) else {
    return 2;
  };
  let Ok(first) = net.udp_socket(any) else {
    return 2;
  };
  let nb1 = net.set_nonblocking(first, true);
  let leg = asked_on(Leg { c: h3q.client(q, h3.config()), sock: first, answer: "", dropped: false }, peer);
  print(leg.answer);
  let Ok(second) = net.udp_socket(any) else {
    return 2;
  };
  let nb2 = net.set_nonblocking(second, true);
  let moved = asked_on(Leg { ...leg, sock: second, answer: "" }, peer);
  print(moved.answer);
  let closed = moved.c.close(clock.instant()).transmit(clock.instant());
  for d in closed.datagrams() {
    let r = net.send_to(moved.sock, d, peer);
  }
  print("answered on the second socket, first connection id retired: " + (moved.answer != "").to_string() + " " + (!has_id(moved.c.quic().local_cids(), moved.c.quic().scid())).to_string());
  if (leg.answer == "" || moved.answer == "") {
    return 1;
  }
  return 0;
}
`
