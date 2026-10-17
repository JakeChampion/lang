package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The serve loop's own allocations per request, #9853 exit criterion A's
// count half: a hello handler served on one keep-alive connection, with
// /count answering __heap_alloc_count(). The framing gate counts the parse
// and the serialize alone; this one counts everything the loop does between
// two requests (the reactor wait, the read, the handler's task, the reply,
// the buffer's compaction), and the handler's own response.

// ServeAllocsRounds is how many hello requests the count spans on a host
// running the server natively; ServeAllocsRoundsEmulated is the count under
// qemu or wasmtime, where a request costs about a millisecond.
const ServeAllocsRounds = 100000
const ServeAllocsRoundsEmulated = 10000

// serveAllocsWarm is how many requests run before the first count, so the
// connection's buffers and the loop's tables have reached their size.
const serveAllocsWarm = 2000

// serveAllocsH2Drift is how far under its ceiling the HTTP/2 mean may sit
// before the ceiling is lowered. Runs of one build on one machine agree to
// within one allocation, but the mean moves with how the client's frames
// coalesce into the server's reads, and so with the machine: a build that
// read 98.7 to 99.3 here read 95.4 on the CI runner.
const serveAllocsH2Drift = 8

// serveAllocsSlackPerSecond is how far the total may sit from a whole
// number per request, per second the rounds took and two more: the loop
// reformats its Date once a second, about thirty allocations each time.
const serveAllocsSlackPerSecond = 40

// ServeAllocsServerSource is a server answering "hello" to every
// path but /count, which answers the allocator's call count. It serves the
// listener it inherits, or `port` where it inherits none, for `rounds`
// requests and the warm-up on one connection.
func ServeAllocsServerSource(port, rounds int) string {
	return serveAllocsSource(port, rounds, "serve.config()")
}

// ServeAllocsH2ServerSource is ServeAllocsServerSource under `with_h2()`,
// for the requests to arrive on one HTTP/2 connection.
func ServeAllocsH2ServerSource(port, rounds int) string {
	return serveAllocsSource(port, rounds, "serve.config().with_h2()")
}

func serveAllocsSource(port, rounds int, config string) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/count") {
        return http.ok(__heap_alloc_count().to_string());
    }
    return http.ok("hello");
}
function main(): i32 {
    return serve.run(%d, serve.Config { ...%s, keep_alive_requests: %d }, handle);
}
`, port, config, serveAllocsWarm+rounds+8)
}

// CheckServeAllocs drives ServeAllocsServerSource at addr for rounds hello
// requests and holds the allocations per request to want. Two counts back
// to back give what a count request costs, which is taken off the count
// around the rounds. The pin is a ratchet: above it is a regression, below
// it lowers the pin.
func CheckServeAllocs(t *testing.T, addr string, want int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	get := func(path string) string {
		if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost:8080\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\n\r\n", path); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s answered %d", path, resp.StatusCode)
		}
		return string(body)
	}
	total, seconds := serveAllocsRounds(t, get, rounds)
	got := int64(math.Round(float64(total) / float64(rounds)))
	if d := total - got*int64(rounds); d < -serveAllocsSlackPerSecond*seconds || d > serveAllocsSlackPerSecond*seconds {
		t.Fatalf("%d allocations over %d requests is %d from a whole number per request", total, rounds, d)
	}
	if got != want {
		t.Errorf("the serve loop allocates %d times per hello request; pinned %d.\n"+
			"Above the pin is a regression. Below it, lower the pin in the same change.", got, want)
	}
}

// CheckServeAllocsH2 is CheckServeAllocs over one prior-knowledge HTTP/2
// connection (H2CClient), against ServeAllocsH2ServerSource. The count
// per request varies there, with the blocks the connection's frames,
// header blocks and stream slots take and give back and with how the
// client's frames coalesce into reads, so `ceiling` bounds the mean: above
// it is a regression, and a mean serveAllocsH2Drift or more under it
// lowers the ceiling.
func CheckServeAllocsH2(t *testing.T, addr string, ceiling int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	client, err := DialH2C(addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Minute))
	get := func(path string) string {
		body, err := client.Get(path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return body
	}
	total, seconds := serveAllocsRounds(t, get, rounds)
	mean := float64(total) / float64(rounds)
	if total > ceiling*int64(rounds)+serveAllocsSlackPerSecond*seconds {
		t.Errorf("the serve loop allocates %.2f times per HTTP/2 hello request; the ceiling is %d. Above it is a regression.", mean, ceiling)
	}
	if total < (ceiling-serveAllocsH2Drift)*int64(rounds) {
		t.Errorf("the serve loop allocates %.2f times per HTTP/2 hello request, %d or more under the ceiling of %d: lower the ceiling in the same change.", mean, serveAllocsH2Drift, ceiling)
	}
}

// serveAllocsRounds warms the connection `get` speaks over and counts
// around `rounds` hello requests: the allocations they cost, with what the
// count requests themselves cost taken off, and the seconds they took plus
// two, the loop's Date reformatting about thirty allocations once a second.
func serveAllocsRounds(t *testing.T, get func(path string) string, rounds int) (total, seconds int64) {
	t.Helper()
	count := func() int64 {
		n, err := strconv.ParseInt(get("/count"), 10, 64)
		if err != nil {
			t.Fatalf("/count: %v", err)
		}
		return n
	}
	for i := 0; i < serveAllocsWarm; i++ {
		if body := get("/"); body != "hello" {
			t.Fatalf("hello request %d answered %q", i, body)
		}
	}
	c0 := count()
	c1 := count()
	started := time.Now()
	for i := 0; i < rounds; i++ {
		get("/")
	}
	c2 := count()
	seconds = int64(time.Since(started)/time.Second) + 2
	total = c2 - c1 - (c1 - c0)
	t.Logf("counts %d, %d, %d: %d allocations over %d requests in %d s", c0, c1, c2, total, rounds, seconds-2)
	return total, seconds
}

// ServeAllocsH3ServerSource is ServeAllocsServerSource under TLS and
// `with_h3()`, for the requests to arrive on one HTTP/3 connection. The
// identity is the Ed25519 leaf tests/stdlib/http_h3q_test.fern uses, so no
// file is written.
func ServeAllocsH3ServerSource(port, rounds int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/hex";
import "std/tls/identity";
import "std/tls/server";
%s
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/count") {
        return http.ok(__heap_alloc_count().to_string());
    }
    return http.ok("hello");
}
function main(): i32 {
    let Ok(k) = identity.private_key_from_pem(ED_PKCS8) else { return 2; };
    let Ok(id) = identity.pair([hex.hex_decode(ED_LEAF)], k) else { return 3; };
    return serve.run(%d, serve.Config { ...serve.config().with_tls(server.config(id)).with_h3(), keep_alive_requests: %d }, handle);
}
`, serveAllocsH3Identity, port, serveAllocsWarm+rounds+8)
}

// ServeAllocsH3ClientSource is the client of CheckServeAllocsH3: one
// HTTP/3 connection to 127.0.0.1 on the port in its first argument, the
// warm-up, then its second argument's rounds of hello requests one at a
// time, counted as serveAllocsRounds counts them. It prints
// `allocs TOTAL SECONDS`.
var ServeAllocsH3ClientSource = `import "std/net";
import "std/quic/conn";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/verify";
import "std/headers";
import "std/async";
import "std/option";
import "std/string";
import "std/hex";
import "std/i64";
` + serveAllocsH3Identity + `
function now(): i64 {
  return monotonic_ns();
}

function request(path: string): HttpRequest {
  return HttpRequest { method: "GET", path: path, body: Stream { data: [], pos: 0, source: None }, headers: headers.header_map_new().append("host", "tls.example"), trailers: headers.header_map_new() };
}

struct Cli { c: h3q.Conn, sock: i32, peer: net.SocketAddr, buf: u8[] }

function pump(k: Cli, ms: i32): Cli {
  let c2: h3q.Conn = k.c.transmit(now());
  let out: u8[][] = c2.datagrams();
  c2 = c2.sent();
  let c: h3q.Conn = c2;
  for d in out {
    let r: Result[i32, net.NetError] = net.send_to(k.sock, d, k.peer);
  }
  let woke: i32 = async.wait_any([k.sock, 1], ms);
  let going: boolean = true;
  while (going) {
    match (net.recv_from(k.sock, k.buf)) {
      Ok(got) => {
        c = c.receive(k.buf[0:got.0], now());
      },
      Err(_) => {
        going = false;
      }
    }
  }
  if (c.deadline() != 0 && now() >= c.deadline()) {
    c = c.tick(now());
  }
  return Cli { ...k, c: c };
}

// get sends one request and pumps until its response arrives.
function get(k0: Cli, path: string): (Cli, string) {
  let Ok(sent) = k0.c.request(request(path), "https") else {
    return (k0, "ERR request");
  };
  let k: Cli = Cli { ...k0, c: sent.0 };
  let id: i64 = sent.1;
  let started: i64 = now();
  while (now() - started < 30000000000) {
    k = pump(k, 10);
    let evs: h3q.Event[] = k.c.events;
    let c: h3q.Conn = k.c.events_taken();
    k = Cli { ...k, c: c };
    for e in evs {
      match (e) {
        h3q.Responded(rid, resp) => {
          if (rid == id) {
            return (k, string_from_bytes_unchecked(resp.body_bytes()));
          }
        },
        _ => {}
      }
    }
  }
  return (k, "ERR timeout");
}

function count(k: Cli): (Cli, i64) {
  let (k2, body) = get(k, "/count");
  return (k2, body.parse_int().unwrap_or(0 - 1) as i64);
}

function main(): i32 {
  let argv: string[] = args();
  let Some(peer) = net.socket_addr_parse("127.0.0.1:" + argv[1]) else {
    return 2;
  };
  let Some(rounds) = argv[2].parse_int() else {
    return 2;
  };
  let cfg: conn.Config = h3q.transport(conn.config([]).client_side("tls.example", verify.roots([hex.hex_decode(ROOT)])));
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
  let k: Cli = Cli { c: h3q.client(q, h3.config()), sock: sock, peer: peer, buf: __alloc_u8(65536) };
  let started: i64 = now();
  while (!k.c.ready() && now() - started < 30000000000) {
    k = pump(k, 10);
  }
  if (!k.c.ready()) {
    print("handshake failed");
    return 1;
  }
  let i: i32 = 0;
  while (i < ` + strconv.Itoa(serveAllocsWarm) + `) {
    let r: (Cli, string) = get(k, "/");
    k = r.0;
    if (r.1 != "hello") {
      print("warm request " + i.to_string() + " answered " + r.1);
      return 1;
    }
    i = i + 1;
  }
  let (k1, c0) = count(k);
  let (k2, c1) = count(k1);
  k = k2;
  let t0: i64 = now();
  i = 0;
  while (i < rounds) {
    let r: (Cli, string) = get(k, "/");
    k = r.0;
    if (r.1 != "hello") {
      print("request " + i.to_string() + " answered " + r.1);
      return 1;
    }
    i = i + 1;
  }
  let seconds: i64 = (now() - t0) / 1000000000 + 2;
  let (k3, c2) = count(k);
  k = k3;
  if (c0 < 0 || c1 < 0 || c2 < 0) {
    print("count failed");
    return 1;
  }
  print("allocs " + (c2 - c1 - (c1 - c0)).to_string() + " " + seconds.to_string());
  let closed: h3q.Conn = k.c.close(now()).transmit(now());
  let out: u8[][] = closed.datagrams();
  closed = closed.sent();
  for d in out {
    let r: Result[i32, net.NetError] = net.send_to(sock, d, peer);
  }
  return 0;
}
`

// CheckServeAllocsH3 runs `client`, a ServeAllocsH3ClientSource build with
// its arguments set, and holds the mean it reports under `ceiling` as
// CheckServeAllocsH2 does: above it is a regression, and a mean
// serveAllocsH2Drift or more under it lowers the ceiling.
func CheckServeAllocsH3(t *testing.T, addr string, client *exec.Cmd, ceiling int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	out, err := client.CombinedOutput()
	if err != nil {
		t.Fatalf("h3 client: %v\n%s", err, out)
	}
	var total, seconds int64
	if _, err := fmt.Sscanf(string(out), "allocs %d %d", &total, &seconds); err != nil {
		t.Fatalf("h3 client printed %q, want `allocs TOTAL SECONDS`", out)
	}
	mean := float64(total) / float64(rounds)
	t.Logf("%d allocations over %d HTTP/3 requests in %d s: %.2f per request", total, rounds, seconds-2, mean)
	if total > ceiling*int64(rounds)+serveAllocsSlackPerSecond*seconds {
		t.Errorf("the serve loop allocates %.2f times per HTTP/3 hello request; the ceiling is %d. Above it is a regression.", mean, ceiling)
	}
	if total < (ceiling-serveAllocsH2Drift)*int64(rounds) {
		t.Errorf("the serve loop allocates %.2f times per HTTP/3 hello request, %d or more under the ceiling of %d: lower the ceiling in the same change.", mean, serveAllocsH2Drift, ceiling)
	}
}

// serveAllocsH3Identity is the test root, the Ed25519 leaf for tls.example
// it signed, and the leaf's key, as tests/stdlib/http_h3q_test.fern holds
// them.
const serveAllocsH3Identity = `const ROOT: string = "3082011c3081cfa00302010202020201300506032b657030243122302006035504030c194665726e20544c5320536572766572205465737420526f6f743020170d3230303130313030303030305a180f32313030303130313030303030305a30243122302006035504030c194665726e20544c5320536572766572205465737420526f6f74302a300506032b6570032100d04ab232742bb4ab3a1368bd4615e4e6d0224ab71a016baf8520a332c9778737a3233021300f0603551d130101ff040530030101ff300e0603551d0f0101ff040403020106300506032b65700341007353a91b5ad2e713d51d2fa08f3e800b99afa9e5725cfd746269d03210b1faadccfc942258bb8151da41be07943a4e8a14bd615e96692c8b896872460e827c0a";
const ED_LEAF: string = "3082013e3081f1a00302010202020204300506032b657030243122302006035504030c194665726e20544c5320536572766572205465737420526f6f743020170d3230303130313030303030305a180f32313030303130313030303030305a30163114301206035504030c0b746c732e6578616d706c65302a300506032b6570032100a09aa5f47a6759802ff955f8dc2d2a14a5c99d23be97f864127ff9383455a4f0a3533051300c0603551d130101ff04023000300e0603551d0f0101ff04040302078030130603551d25040c300a06082b06010505070301301c0603551d1104153013820b746c732e6578616d706c658704c0000207300506032b6570034100bdd423797a497344c22bf704b418f31b7b6d0d884b517a57d1421c064fa68de8266518e0ddedfbdf189a71a2bb5bd2ffb7ca885f2a7f9ac0e96cb6d07d83e802";
const ED_PKCS8: string = "-----BEGIN PRIVATE KEY-----\n" + "MC4CAQAwBQYDK2VwBCIEICIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIi\n" + "-----END PRIVATE KEY-----\n";
`
