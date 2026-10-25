package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestServeH3SocketX86_64(t *testing.T) {
	w := newServeH3World(t)
	base, err := os.ReadFile(w.src)
	if err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, name, src string) string {
		t.Helper()
		file := filepath.Join(w.dir, name+".fern")
		bin := filepath.Join(w.dir, name+".bin")
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", bin, file).CombinedOutput(); err != nil {
			t.Fatalf("%s build: %v\n%s", name, err, out)
		}
		return bin
	}
	client := build(t, "socket_client", serveH3SocketClient)
	server := build(t, "socket_server", string(base))
	t.Run("ipv4", func(t *testing.T) {
		addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(w.qemu, server))
		_, port, _ := net.SplitHostPort(addr)
		out, err := runX86Bin(w.qemu, client, "127.0.0.1:"+port, "127.0.0.1:0", w.ca).CombinedOutput()
		if err != nil || string(out) != "hello /hello\n" {
			t.Fatalf("IPv4 HTTP/3: %v\n%s", err, out)
		}
	})
	t.Run("ipv6", func(t *testing.T) {
		if !e2eharness.HostHasIPv6() {
			t.Skip("the host has no IPv6 loopback")
		}
		ln, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback})
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		file, err := ln.File()
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		cmd := runX86Bin(w.qemu, server)
		cmd.Env = append(cmd.Environ(), "LISTEN_FDS=1")
		e2eharness.StartServerProcess(t, cmd, file)
		port := ln.Addr().(*net.TCPAddr).Port
		out, err := runX86Bin(w.qemu, client, fmt.Sprintf("[::1]:%d", port), "[::1]:0", w.ca).CombinedOutput()
		if err != nil || string(out) != "hello /hello\n" {
			t.Fatalf("IPv6 HTTP/3: %v\n%s", err, out)
		}
	})
	t.Run("two_workers", func(t *testing.T) {
		src := string(base)
		start := strings.Index(src, "function handle(")
		if start < 0 {
			t.Fatal("missing server handler")
		}
		end := strings.Index(src[start:], "\nfunction main()") + start
		if end < start {
			t.Fatal("missing server handler")
		}
		src = src[:start] + `function worker(id: string, req: HttpRequest, plat: platform.Host): (string, HttpResponse) {
  let key: string = id;
  if (key == "") {
    key = plat.random_i32().to_string() + ":" + plat.random_i32().to_string() + ":" + plat.random_i32().to_string() + ":" + plat.random_i32().to_string();
  }
  return (key, http.ok(key));
}
` + src[end:]
		src = strings.Replace(src, "serve.run(0, serve.config().with_tls(server.config(id, random_bytes(32))).with_h3(), handle)", "serve.supervise_with(0, serve.Config { ...serve.config().with_tls(server.config(id, random_bytes(32))).with_h3(), workers: 2 }, \"\", worker)", 1)
		bin := build(t, "socket_workers", src)
		addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(w.qemu, bin))
		_, port, _ := net.SplitHostPort(addr)
		seen := map[string]bool{}
		for i := 0; i < 32 && len(seen) < 2; i++ {
			out, err := runX86Bin(w.qemu, client, "127.0.0.1:"+port, "127.0.0.1:0", w.ca).CombinedOutput()
			if err != nil {
				t.Fatalf("connection %d: %v\n%s", i, err, out)
			}
			id := strings.TrimSpace(string(out))
			if len(strings.Split(id, ":")) != 4 {
				t.Fatalf("worker identity = %q", id)
			}
			seen[id] = true
		}
		if len(seen) != 2 {
			t.Fatalf("independent connections reached %d worker(s), want 2", len(seen))
		}
	})
}

const serveH3SocketClient = `import "std/net";
import "std/quic/conn";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/headers";
import "std/async";
import "std/option";
import "std/string";

function main(): i32 {
  let argv: string[] = args();
  let Some(peer) = net.socket_addr_parse(argv[1]) else { return 2; };
  let Some(local) = net.socket_addr_parse(argv[2]) else { return 2; };
  let Ok(roots) = verify.roots_from_file(argv[3]) else { return 2; };
  let cfg: conn.Config = h3q.transport(conn.config([]).client_side("localhost", roots));
  let Ok(q) = conn.client(cfg, random_bytes(conn.client_entropy(cfg)), monotonic_ns(), now_unix_ms()) else { return 2; };
  let Ok(sock) = net.udp_socket(local) else { return 2; };
  let nb: Result[void, net.NetError] = net.set_nonblocking(sock, true);
  let c: h3q.Conn = h3q.client(q, h3.config());
  let buf: u8[] = __alloc_u8(65536);
  let sent: i32 = 0;
  let got: i32 = 0;
  let first: string = "";
  let started: i64 = monotonic_ns();
  while (!c.done() && monotonic_ns() - started < 15000000000) {
    let now: i64 = monotonic_ns();
    let next: h3q.Conn = c.transmit(now);
    let out: u8[][] = next.datagrams();
    next = next.sent();
    c = next;
    for d in out { let r: Result[i32, net.NetError] = net.send_to(sock, d, peer); }
    let woke: i32 = async.wait_any([sock, 1], 10);
    let reading: boolean = true;
    while (reading) {
      match (net.recv_from(sock, buf)) {
        Ok(d) => { c = c.receive(buf[0:d.0], monotonic_ns()); },
        Err(_) => { reading = false; }
      }
    }
    if (c.deadline() != 0 && monotonic_ns() >= c.deadline()) { c = c.tick(monotonic_ns()); }
    let evs: h3q.Event[] = c.events;
    let after: h3q.Conn = c.events_taken();
    c = after;
    for e in evs {
      match (e) {
        h3q.Responded(_, resp) => {
          if (resp.status != 200) { return 3; }
          let text: string = string_from_bytes_unchecked(resp.body_bytes());
          if (got == 0) { first = text; } else { if (text != first) { return 4; } }
          got = got + 1;
        },
        h3q.StreamDropped(_, _) => { return 5; },
        _ => {}
      }
    }
    if (got == 2) {
      let closed: h3q.Conn = c.close(monotonic_ns()).transmit(monotonic_ns());
      let final: u8[][] = closed.datagrams();
      closed = closed.sent();
      for d in final { let r: Result[i32, net.NetError] = net.send_to(sock, d, peer); }
      let shut: Result[void, net.NetError] = net.close(sock);
      print(first);
      return 0;
    }
    if (c.ready() && sent == got) {
      let req: HttpRequest = HttpRequest { method: "GET", path: "/hello", body: ByteStream { data: [], pos: 0, source: None }, headers: headers.header_map_new().append("host", "localhost"), trailers: headers.header_map_new() };
      let Ok(asked) = c.request(req, "https") else { return 6; };
      c = asked.0;
      sent = sent + 1;
    }
  }
  eprint("HTTP/3 socket request timed out");
  return 1;
}
`
