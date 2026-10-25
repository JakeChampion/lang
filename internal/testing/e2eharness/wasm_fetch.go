package e2eharness

import (
	"fmt"
	"net"
	"testing"
	"time"
)

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

const wasmFetchPrelude = `import "std/async";
import "std/fetch";

function parse(s: string): i32 {
    let n: i32 = 0; let i: i32 = 0;
    while (i < s.len()) { let b: i32 = s[i] as i32; if (b < 48 || b > 57) { return 0; } n = n * 10 + (b - 48); i = i + 1; }
    return n;
}
function port(key: string): i32 { match (env(key)) { Some(s) => { return parse(s); }, None => { return 0; } } }
function body_at(key: string, path: string): string {
    match (fetch.send(fetch.get("http://127.0.0.1:" + port(key).to_string() + path))) {
        Ok(resp) => { return resp.body_string(); },
        Err(e) => { return e.message(); }
    }
    return "";
}
`

const WasmFetchFanoutSource = wasmFetchPrelude + `
function main(): i32 {
    let both: (() => string)[] = [() => body_at("PSLOW", "/a"), () => body_at("PFAST", "/b")];
    let bodies: string[] = async.gather_tasks(both, "incomplete");
    if (bodies.len() != 2) { return 90; }
    print(bodies[0]);   // task 0 (slow upstream) → "AAA"
    print(bodies[1]);   // task 1 (fast upstream) → "BBB"
    return 0;
}`

const WasmRaceFetchSource = wasmFetchPrelude + `
function main(): i32 {
    let both: (() => string)[] = [() => body_at("PA", "/a"), () => body_at("PB", "/b")];
    let (winner, body) = async.race_tasks(both, "none");
    if (winner < 0) { print("nowinner\n"); return 1; }
    print(body);   // the winner's body ("AAA" or "BBB")
    return 0;
}`
