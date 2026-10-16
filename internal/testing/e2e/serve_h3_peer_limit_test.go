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

// An HTTP/3 connection counts against its source IP, not its source port.
// Positive controls distinguish admission rejection from a broken client.
func TestServeH3PeerLimitX86_64(t *testing.T) {
	w := newServeH3World(t)
	clientSrc := filepath.Join(w.dir, "admission.fern")
	clientBin := filepath.Join(w.dir, "admission.bin")
	if err := os.WriteFile(clientSrc, []byte(serveH3AdmissionClient), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", clientBin, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("admission client build: %v\n%s", err, out)
	}
	base, err := os.ReadFile(w.src)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, secondIP string
		limit          int
		secondReady    bool
	}{
		{"same_ip_different_ports", "127.0.0.1", 1, false},
		{"room_for_two", "127.0.0.1", 2, true},
		{"limit_disabled", "127.0.0.1", 0, true},
		{"different_ip", "127.0.0.2", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "server.fern")
			bin := filepath.Join(dir, "server.bin")
			cfg := fmt.Sprintf("serve.Config { ...serve.config(), max_connections_per_ip: %d }", tc.limit)
			if err := os.WriteFile(src, []byte(strings.Replace(string(base), "serve.config()", cfg, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("server build: %v\n%s", err, out)
			}
			addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(w.qemu, bin))
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatal(err)
			}
			out, err := runX86Bin(w.qemu, clientBin, port, w.ca, tc.secondIP).CombinedOutput()
			if err != nil {
				t.Fatalf("admission client: %v\n%s", err, out)
			}
			if want := fmt.Sprintf("first=true second=%t\n", tc.secondReady); string(out) != want {
				t.Errorf("admission got %q, want %q", out, want)
			}
		})
	}
}

const serveH3AdmissionClient = `import "std/net";
import "std/quic/conn";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/async";
import "std/string";
import "std/option";
import "std/result";

function added(cs: h3q.Conn[], socks: i32[], cfg: conn.Config, ip: string): Result[(h3q.Conn[], i32[]), string] {
  let Some(addr) = net.socket_addr_parse(ip + ":0") else {
    return Err("bad bind address");
  };
  let Ok(sock) = net.udp_socket(addr) else {
    return Err("bind failed");
  };
  let Ok(_) = net.set_nonblocking(sock, true) else {
    return Err("nonblocking failed");
  };
  let q: conn.Conn = conn.client(cfg, random_bytes(conn.client_entropy(cfg)), monotonic_ns())?;
  return Ok((cs.append(h3q.client(q, h3.config())), socks.append(sock)));
}

function driven(cs: h3q.Conn[], socks: i32[], peer: net.SocketAddr, buf: u8[], now: i64): h3q.Conn[] {
  let cur: h3q.Conn[] = cs;
  let i: i32 = 0;
  while (i < cur.len()) {
    let c: h3q.Conn = cur[i];
    let (next, out) = c.transmit(now);
    c = next;
    for d in out {
      let sent: Result[i32, net.NetError] = net.send_to(socks[i], d, peer);
    }
    let reading: boolean = true;
    while (reading) {
      match (net.recv_from(socks[i], buf)) {
        Ok(got) => {
          c = c.receive(buf[0:got.0], now);
        },
        Err(_) => {
          reading = false;
        }
      }
    }
    if (c.deadline() != 0 && now >= c.deadline()) {
      c = c.tick(now);
    }
    cur = cur.with(i, c);
    i = i + 1;
  }
  return cur;
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
  let Ok(first) = added([], [], cfg, "127.0.0.1") else {
    return 3;
  };
  let cs: h3q.Conn[] = first.0;
  let socks: i32[] = first.1;
  let buf: u8[] = __alloc_u8(65536);
  let started: i64 = monotonic_ns();
  let second_at: i64 = 0 as i64;
  while (monotonic_ns() - started < 60000000000) {
    let now: i64 = monotonic_ns();
    cs = driven(cs, socks, peer, buf, now);
    if (cs.len() == 1 && cs[0].ready()) {
      let Ok(second) = added(cs, socks, cfg, argv[3]) else {
        return 4;
      };
      cs = second.0;
      socks = second.1;
      second_at = now;
    }
    if (cs.len() == 2 && (cs[1].ready() || now - second_at >= 8000000000)) {
      if (!cs[0].ready()) {
        return 5;
      }
      print("first=true second=" + cs[1].ready().to_string());
      return 0;
    }
    let woke: i32 = async.wait_any([socks[0], 1], 10);
  }
  eprint("first handshake did not complete");
  return 1;
}
`
