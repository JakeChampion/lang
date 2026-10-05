package e2eharness

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// StartDelayedUpstream listens on a loopback port and answers each
// connection, after reading the request and waiting delay, with a 200
// carrying body. It returns the port.
func StartDelayedUpstream(t *testing.T, body string, delay time.Duration) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 256)
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, _ = conn.Read(buf)
				time.Sleep(delay)
				_, _ = conn.Write([]byte(resp))
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// wasmFetchPrelude reads an upstream port from the environment and decodes
// a body, for the two programs below.
const wasmFetchPrelude = `import "std/async";
import "std/fetch";
import "std/string";
import "std/utf8";

function parse(s: string): i32 {
    let n: i32 = 0; let i: i32 = 0;
    while (i < s.len()) { let b: i32 = s[i] as i32; if (b < 48 || b > 57) { return 0; } n = n * 10 + (b - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }
function text(b: u8[]): string { match (utf8.from_bytes(b)) { Some(t) => { return t; }, None => { return "?"; } } }
`

// WasmFetchFanoutSource fans two `fetch_future` reads, from the upstreams
// on PSLOW and PFAST, out through `async.gather` and prints both bodies in
// input order.
const WasmFetchFanoutSource = wasmFetchPrelude + `
function main(): i32 {
    let none: u8[] = [];
    let host: i32 = 127 | (1 << 24);   // 127.0.0.1
    let f1: async.Future[u8[]] = fetch.fetch_future(host, port("PSLOW"), "/a");
    let f2: async.Future[u8[]] = fetch.fetch_future(host, port("PFAST"), "/b");
    let fs: async.Future[u8[]][] = [f1, f2];
    let bodies: u8[][] = async.gather(fs, none);
    if (bodies.len() != 2) { return 90; }
    print(text(bodies[0]));   // task 0 (slow upstream) → "AAA"
    print(text(bodies[1]));   // task 1 (fast upstream) → "BBB"
    return 0;
}`

// WasmRaceFetchSource races two `fetch_future` reads, from the upstreams on
// PA and PB, through `async.race` and prints the winner's body.
const WasmRaceFetchSource = wasmFetchPrelude + `
function main(): i32 {
    let none: u8[] = [];
    let host: i32 = 127 | (1 << 24);
    let fs: async.Future[u8[]][] = [
        fetch.fetch_future(host, port("PA"), "/a"),
        fetch.fetch_future(host, port("PB"), "/b")
    ];
    let (winner, body) = async.race(fs, none);
    if (winner < 0) { print("nowinner\n"); return 1; }
    print(text(body));   // the winner's body ("AAA" or "BBB")
    return 0;
}`
