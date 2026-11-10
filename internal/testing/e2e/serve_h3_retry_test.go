package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A server told to validate every client's address (h3_retry_above: 0)
// answers an Initial with a Retry and keeps no state: a spoofed Initial
// gets a Retry and nothing else, an Initial carrying a token the server
// did not issue gets a CONNECTION_CLOSE with INVALID_TOKEN, and a client
// that echoes the token is served.
func TestServeH3RetryX86_64(t *testing.T) {
	w := newServeH3WorldWithConfig(t, "serve.Config { ...serve.config(), h3_retry_above: 0 }")
	out := filepath.Join(w.dir, "serve_h3.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", out, w.src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	clientSrc := filepath.Join(w.dir, "h3_retry_client.fern")
	if err := os.WriteFile(clientSrc, []byte(serveH3RetryClient), 0o644); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(w.dir, "h3_retry_client.bin")
	if o, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("client build failed: %v\n%s", err, o)
	}
	for _, delayed := range []bool{false, true} {
		name := "immediate"
		if delayed {
			name = "delayed_start"
		}
		t.Run(name, func(t *testing.T) {
			server := runX86Bin(w.qemu, out)
			if delayed {
				args := append([]string{"-c", "sleep 1.5; exec \"$@\"", "h3-start-delay", server.Path}, server.Args[1:]...)
				wrapper := exec.Command("sh", args...)
				wrapper.Env, wrapper.Dir = server.Env, server.Dir
				server = wrapper
			}
			addr, _ := e2eharness.StartInheritedServer(t, server)
			e2eharness.WaitServerReady(t, addr)
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := runX86Bin(w.qemu, client, port, w.ca).CombinedOutput()
			if err != nil {
				t.Fatalf("retry client: %v\n%s", err, got)
			}
			want := "spoofed initial: answers 1, all retry true\n" +
				"bad token: closed remote 11 the token is not one this server issued\n" +
				"GET /hello -> 200 hello /hello, retried true\n"
			if string(got) != want {
				t.Errorf("retry client got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// The client: a bare Initial sent from a socket that then only listens, a
// client that answers a Retry it forged itself, then a real request.
const serveH3RetryClient = `import "std/time";
import "std/clock";
import "std/net";
import "std/quic/conn";
import "std/quic/packet";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/headers";
import "std/async";
import "std/option";
import "std/string";
import "std/utf8";

import "std/rand";
struct Leg { c: h3q.Conn, sock: Socket, answer: string, dropped: boolean }

function request(): HttpRequest {
  return HttpRequest { method: "GET", path: "/hello", body: ByteStream { data: [], pos: 0, source: None }, headers: headers.new().append("host", "localhost"), trailers: headers.new() };
}

// asked_on drives the connection until its GET is answered, the connection
// ends, or the peer drops the stream.
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
          cur = Leg { ...cur, c: cur.c.receive(buf[0:d.0], clock.instant()) };
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

// first_flight is a new client and the datagrams of its first Initial
// flight, which it counts as sent.
function first_flight(cfg: conn.Config): Result[(conn.Conn, u8[][]), string] {
  let q = conn.client(cfg, rand.bytes(conn.client_entropy(cfg)), clock.instant(), clock.now())?;
  let sent = q.transmit(clock.instant());
  return Ok((sent.sent(), sent.datagrams()));
}

// spoofed sends the first Initial of a client that never answers, as an
// Initial from a spoofed address would be, and reports what came back in
// half a second: how many datagrams, and whether every one was a Retry.
function spoofed(cfg: conn.Config, sock: Socket, peer: net.SocketAddr): string {
  let Ok(flight) = first_flight(cfg) else {
    return "spoofed initial: no client";
  };
  let r = net.send_to(sock, flight.1[0], peer);
  let buf: u8[] = __alloc_u8(65536);
  let answers = 0;
  let retries = 0;
  let until = clock.instant().add(time.duration_millis(500));
  while (clock.instant().cmp(until) < 0) {
    let woke = async.wait_any([sock.fd, 1], Some(until));
    let reading = true;
    while (reading) {
      match (net.recv_from(sock, buf)) {
        Ok(d) => {
          answers += 1;
          match (packet.parse(buf[0:d.0], 0, 8)) {
            Ok(h) => {
              match (h.kind) {
                packet.Retry => {
                  retries += 1;
                },
                _ => {}
              }
            },
            Err(_) => {}
          }
        },
        Err(_) => {
          reading = false;
        }
      }
    }
  }
  return "spoofed initial: answers " + answers.to_string() + ", all retry " + (answers > 0 && retries == answers).to_string();
}

// forged_token answers a Retry the client forged itself, so its next
// Initial carries a token the server never issued.
function forged_token(cfg: conn.Config, sock: Socket, peer: net.SocketAddr): string {
  let Ok(flight) = first_flight(cfg) else {
    return "bad token: no client";
  };
  let Ok(first) = packet.parse(flight.1[0], 0, 8) else {
    return "bad token: cannot parse the first Initial";
  };
  let q = flight.0.receive(packet.retry(first.scid, [1u8, 2u8, 3u8, 4u8, 5u8, 6u8, 7u8, 8u8], first.dcid, "nope".bytes()), clock.instant());
  let leg = asked_on(Leg { c: h3q.client(q, h3.config()), sock: sock, answer: "", dropped: false }, peer);
  let Some(why) = leg.c.quic().closed_with() else {
    return "bad token: not closed, answer " + leg.answer;
  };
  let side = "local";
  if (why.remote) {
    side = "remote";
  }
  return "bad token: closed " + side + " " + why.code.to_string() + " " + why.reason;
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
  let Ok(quiet) = net.udp_socket(any) else {
    return 2;
  };
  let nb1 = net.set_nonblocking(quiet, true);
  print(spoofed(cfg, quiet, peer));
  let Ok(forging) = net.udp_socket(any) else {
    return 2;
  };
  let nb2 = net.set_nonblocking(forging, true);
  print(forged_token(cfg, forging, peer));
  let Ok(q) = conn.client(cfg, rand.bytes(conn.client_entropy(cfg)), clock.instant(), clock.now()) else {
    return 2;
  };
  let Ok(sock) = net.udp_socket(any) else {
    return 2;
  };
  let nb3 = net.set_nonblocking(sock, true);
  let leg = asked_on(Leg { c: h3q.client(q, h3.config()), sock: sock, answer: "", dropped: false }, peer);
  let closed = leg.c.close(clock.instant()).transmit(clock.instant());
  for d in closed.datagrams() {
    let r = net.send_to(leg.sock, d, peer);
  }
  print(leg.answer + ", retried " + leg.c.quic().retried().to_string());
  if (leg.answer == "") {
    return 1;
  }
  return 0;
}
`
