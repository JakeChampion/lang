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

const ServeAllocsRounds = 100000
const ServeAllocsRoundsEmulated = 10000

const serveAllocsWarm = 2000

const serveAllocsH2Drift = 8

const serveAllocsSlackPerSecond = 40

func ServeAllocsServerSource(port, rounds int) string {
	return serveAllocsSource(port, rounds, "serve.config()")
}

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
    return serve.exit_code(serve.run(%d, serve.Config { ...%s, keep_alive_requests: %d }, handle));
}
`, port, config, serveAllocsWarm+rounds+8)
}

func CheckServeAllocs(t *testing.T, addr string, want int64, rounds int) {
	t.Helper()
	total, seconds := serveAllocsRounds(t, KeepAliveGET(t, addr), rounds)
	got := int64(math.Round(float64(total) / float64(rounds)))
	if d := total - got*int64(rounds); d < -serveAllocsSlackPerSecond*seconds || d > serveAllocsSlackPerSecond*seconds {
		t.Fatalf("%d allocations over %d requests is %d from a whole number per request", total, rounds, d)
	}
	if got != want {
		t.Errorf("the serve loop allocates %d times per hello request; pinned %d.\n"+
			"Above the pin is a regression. Below it, lower the pin in the same change.", got, want)
	}
}

func KeepAliveGET(t *testing.T, addr string) func(path string) string {
	t.Helper()
	WaitServerReady(t, addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	return func(path string) string {
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
}

func ServeFetchAllocsServerSource(port, rounds int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/fetch";
function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, fetch.FetchError] {
    if (req.path == "/count") {
        return Ok(http.ok(__heap_alloc_count().to_string()));
    }
    let first: HttpResponse = plat.http(fetch.get("http://8.8.8.8/"))?;
    let second: HttpResponse = plat.http(fetch.get("http://8.8.8.8/"))?;
    if (first.status != 200 || second.status != 200) {
        return Ok(http.text(502, ""));
    }
    return Ok(http.ok("hello"));
}
function main(): i32 {
    return serve.exit_code(serve.run(%d, serve.Config { ...serve.config(), keep_alive_requests: %d }, handle));
}
`, port, serveAllocsWarm+rounds+8)
}

func CheckServeFetchAllocs(t *testing.T, addr string, ceiling int64, rounds int) {
	t.Helper()
	total, seconds := serveAllocsRounds(t, KeepAliveGET(t, addr), rounds)
	checkAllocsCeiling(t, "two-fetch", total, seconds, rounds, ceiling)
}

func CheckServeAllocsH2(t *testing.T, addr string, ceiling int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr)
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
	checkAllocsCeiling(t, "HTTP/2 hello", total, seconds, rounds, ceiling)
}

func checkAllocsCeiling(t *testing.T, what string, total, seconds int64, rounds int, ceiling int64) {
	t.Helper()
	mean := float64(total) / float64(rounds)
	if total > ceiling*int64(rounds)+serveAllocsSlackPerSecond*seconds {
		t.Errorf("the serve loop allocates %.2f times per %s request; the ceiling is %d. Above it is a regression.", mean, what, ceiling)
	}
	if total < (ceiling-serveAllocsH2Drift)*int64(rounds) {
		t.Errorf("the serve loop allocates %.2f times per %s request, %d or more under the ceiling of %d: lower the ceiling in the same change.", mean, what, serveAllocsH2Drift, ceiling)
	}
}

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

func ServeAllocsH3ServerSource(port, rounds int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/hex";
import "std/codec";
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
    let Ok(id) = identity.pair([__codec_bytes(hex.decode(ED_LEAF))], k) else { return 3; };
    return serve.exit_code(serve.run(%d, serve.Config { ...serve.config().with_tls(server.config(id, random_bytes(32))).with_h3(), keep_alive_requests: %d }, handle));
}

// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
}
`, serveAllocsH3Identity, port, serveAllocsWarm+rounds+8)
}

const serveAllocsH3ClientCommon = `import "std/net";
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
` + serveAllocsH3Identity + `import "std/i32";
import "std/result";

function now(): i64 {
  return monotonic_ns();
}

function request(path: string): HttpRequest {
  return HttpRequest { method: "GET", path: path, body: ByteStream { data: [], pos: 0, source: None }, headers: headers.header_map_new().append("host", "tls.example"), trailers: headers.header_map_new() };
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
  return (k2, i32.parse_i32(body).unwrap_or(0 - 1) as i64);
}
`

// ServeAllocsH3ClientSource drives the server serially: one HTTP/3
// connection to 127.0.0.1 on the port in its first argument, the warm-up,
// then its second argument's rounds of hello requests one at a time,
// counted as serveAllocsRounds counts them. It prints `allocs TOTAL SECONDS`.
var ServeAllocsH3ClientSource = serveAllocsH3ClientCommon + `import "std/i32";

function main(): i32 {
  let argv: string[] = args();
  let Some(peer) = net.socket_addr_parse("127.0.0.1:" + argv[1]) else {
    return 2;
  };
  let Ok(rounds) = i32.parse_i32(argv[2]) else {
    return 2;
  };
  let Ok(root) = hex.decode(ROOT) else { return 2; };
  let cfg: conn.Config = h3q.transport(conn.config([]).client_side("tls.example", verify.roots([root])));
  let Ok(q) = conn.client(cfg, random_bytes(conn.client_entropy(cfg)), now(), now_unix_ms()) else {
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

// ServeAllocsH3MuxClientSource is ServeAllocsH3ClientSource with up to its
// third argument's requests in flight on the one connection at a time
// (`drive`), the multiplexed path the 1x256 nightly row exercises. It
// prints `allocs TOTAL SECONDS peak PEAK`, peak the most it held at once:
// the server grants 100 request streams, so a higher argument tops out
// there.
var ServeAllocsH3MuxClientSource = serveAllocsH3ClientCommon + `import "std/i32";

// drive keeps up to m hello requests in flight until rounds are answered,
// pumping the socket between top-ups, and returns the peak held at once and
// how many were answered. Fewer than rounds means the 30-second no-progress
// window expired: the connection stalled, and the caller must not read the
// allocation count as a measurement of rounds requests.
function drive(k0: Cli, rounds: i32, m: i32): (Cli, i32, i32) {
  let k: Cli = k0;
  let completed: i32 = 0;
  let inflight: i32 = 0;
  let peak: i32 = 0;
  let last: i64 = now();
  while (completed < rounds) {
    let topping: boolean = true;
    while (topping) {
      if (inflight >= m || completed + inflight >= rounds || !k.c.quic().can_open(false)) {
        topping = false;
      } else {
        match (k.c.request(request("/"), "https")) {
          Ok(sent) => {
            k = Cli { ...k, c: sent.0 };
            inflight = inflight + 1;
          },
          Err(_) => {
            topping = false;
          }
        }
      }
    }
    if (inflight > peak) {
      peak = inflight;
    }
    k = pump(k, 10);
    let evs: h3q.Event[] = k.c.events;
    k = Cli { ...k, c: k.c.events_taken() };
    for e in evs {
      match (e) {
        h3q.Responded(rid, resp) => {
          inflight = inflight - 1;
          completed = completed + 1;
          last = now();
        },
        _ => {}
      }
    }
    if (now() - last > 30000000000) {
      return (k, peak, completed);
    }
  }
  return (k, peak, completed);
}

function main(): i32 {
  let argv: string[] = args();
  let Some(peer) = net.socket_addr_parse("127.0.0.1:" + argv[1]) else {
    return 2;
  };
  let Ok(rounds) = i32.parse_i32(argv[2]) else {
    return 2;
  };
  let Ok(m) = i32.parse_i32(argv[3]) else {
    return 2;
  };
  let Ok(root) = hex.decode(ROOT) else { return 2; };
  let cfg: conn.Config = h3q.transport(conn.config([]).client_side("tls.example", verify.roots([root])));
  let Ok(q) = conn.client(cfg, random_bytes(conn.client_entropy(cfg)), now(), now_unix_ms()) else {
    return 2;
  };
  let Some(any) = net.socket_addr_parse("127.0.0.1:0") else {
    return 2;
  };
  let Ok(sock) = net.udp_socket(any) else {
    return 2;
  };
  let nb: Result[void, net.NetError] = net.set_nonblocking(sock, true);
  let warm: i32 = ` + strconv.Itoa(serveAllocsWarm) + `;
  let k: Cli = Cli { c: h3q.client(q, h3.config()), sock: sock, peer: peer, buf: __alloc_u8(65536) };
  let started: i64 = now();
  while (!k.c.ready() && now() - started < 30000000000) {
    k = pump(k, 10);
  }
  if (!k.c.ready()) {
    print("handshake failed");
    return 1;
  }
  let w: (Cli, i32, i32) = drive(k, warm, m);
  k = w.0;
  if (w.2 < warm) {
    print("stalled during warm-up: " + w.2.to_string() + " of " + warm.to_string() + " answered");
    return 1;
  }
  let (k1, c0) = count(k);
  let (k2, c1) = count(k1);
  k = k2;
  let t0: i64 = now();
  let d: (Cli, i32, i32) = drive(k, rounds, m);
  k = d.0;
  let peak: i32 = d.1;
  if (d.2 < rounds) {
    print("stalled: " + d.2.to_string() + " of " + rounds.to_string() + " requests answered");
    return 1;
  }
  let seconds: i64 = (now() - t0) / 1000000000 + 2;
  let (k3, c2) = count(k);
  k = k3;
  if (c0 < 0 || c1 < 0 || c2 < 0) {
    print("count failed");
    return 1;
  }
  print("allocs " + (c2 - c1 - (c1 - c0)).to_string() + " " + seconds.to_string() + " peak " + peak.to_string());
  return 0;
}
`

func CheckServeAllocsH3(t *testing.T, addr string, client *exec.Cmd, ceiling int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr)
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

func CheckServeAllocsH3Mux(t *testing.T, addr string, client *exec.Cmd, ceiling int64, rounds int, minPeak int) {
	t.Helper()
	WaitServerReady(t, addr)
	out, err := client.CombinedOutput()
	if err != nil {
		t.Fatalf("h3 mux client: %v\n%s", err, out)
	}
	var total, seconds int64
	var peak int
	if _, err := fmt.Sscanf(string(out), "allocs %d %d peak %d", &total, &seconds, &peak); err != nil {
		t.Fatalf("h3 mux client printed %q, want `allocs TOTAL SECONDS peak PEAK`", out)
	}
	mean := float64(total) / float64(rounds)
	t.Logf("%d allocations over %d multiplexed HTTP/3 requests in %d s: %.2f per request, peak %d in flight", total, rounds, seconds-2, mean, peak)
	if peak < minPeak {
		t.Fatalf("the client held at most %d requests in flight; want at least %d, else the measurement is the serial path", peak, minPeak)
	}
	if total > ceiling*int64(rounds)+serveAllocsSlackPerSecond*seconds {
		t.Errorf("the serve loop allocates %.2f times per multiplexed HTTP/3 hello request; the ceiling is %d. Above it is a regression.", mean, ceiling)
	}
	if total < (ceiling-serveAllocsH2Drift)*int64(rounds) {
		t.Errorf("the serve loop allocates %.2f times per multiplexed HTTP/3 hello request, %d or more under the ceiling of %d: lower the ceiling in the same change.", mean, serveAllocsH2Drift, ceiling)
	}
}

const serveAllocsH3Identity = `const ROOT: string = "3082011c3081cfa00302010202020201300506032b657030243122302006035504030c194665726e20544c5320536572766572205465737420526f6f743020170d3230303130313030303030305a180f32313030303130313030303030305a30243122302006035504030c194665726e20544c5320536572766572205465737420526f6f74302a300506032b6570032100d04ab232742bb4ab3a1368bd4615e4e6d0224ab71a016baf8520a332c9778737a3233021300f0603551d130101ff040530030101ff300e0603551d0f0101ff040403020106300506032b65700341007353a91b5ad2e713d51d2fa08f3e800b99afa9e5725cfd746269d03210b1faadccfc942258bb8151da41be07943a4e8a14bd615e96692c8b896872460e827c0a";
const ED_LEAF: string = "3082013e3081f1a00302010202020204300506032b657030243122302006035504030c194665726e20544c5320536572766572205465737420526f6f743020170d3230303130313030303030305a180f32313030303130313030303030305a30163114301206035504030c0b746c732e6578616d706c65302a300506032b6570032100a09aa5f47a6759802ff955f8dc2d2a14a5c99d23be97f864127ff9383455a4f0a3533051300c0603551d130101ff04023000300e0603551d0f0101ff04040302078030130603551d25040c300a06082b06010505070301301c0603551d1104153013820b746c732e6578616d706c658704c0000207300506032b6570034100bdd423797a497344c22bf704b418f31b7b6d0d884b517a57d1421c064fa68de8266518e0ddedfbdf189a71a2bb5bd2ffb7ca885f2a7f9ac0e96cb6d07d83e802";
const ED_PKCS8: string = "-----BEGIN PRIVATE KEY-----\n" + "MC4CAQAwBQYDK2VwBCIEICIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIi\n" + "-----END PRIVATE KEY-----\n";
`
