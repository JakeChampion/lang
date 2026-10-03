package e2eharness

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// LoopbackHostBE is 127.0.0.1 in network byte order, the form
// `fetch.fetch_future` takes its host in: 127 | (1 << 24).
const LoopbackHostBE = 127 | (1 << 24)

// StartBodyUpstream listens on the loopback interface and answers every
// connection's first request with a 200 carrying body, then closes. It
// returns the port.
func StartBodyUpstream(t *testing.T, body []byte) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 256)
				_, _ = c.Read(b)
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// FetchFutureFanoutSource is a program that fans two `fetch.fetch_future`
// calls to the loopback upstream at port out through `async.gather` and
// exits 42 when both bodies read "hello-world", 85 otherwise.
func FetchFutureFanoutSource(port int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";
import "std/utf8";

function main(): i32 {
    let none: u8[] = [];
    let f1: async.Future[u8[]] = fetch.fetch_future(%d, %d, "/1");
    let f2: async.Future[u8[]] = fetch.fetch_future(%d, %d, "/2");
    let fs: async.Future[u8[]][] = [f1, f2];
    let bodies: u8[][] = async.gather(fs, none);
    let b0: boolean = false;
    let b1: boolean = false;
    match (utf8.from_bytes(bodies[0])) { Some(t) => { b0 = t == "hello-world"; }, None => {}, }
    match (utf8.from_bytes(bodies[1])) { Some(t) => { b1 = t == "hello-world"; }, None => {}, }
    if (b0 && b1) { return 42; }
    return 85;
}`, LoopbackHostBE, port, LoopbackHostBE, port)
}

// FetchFutureLargeBodySource is a program that reads one `bodyLen`-byte
// body through `fetch.fetch_future` and exits 42 when all of it came
// back, else a small code from the length it got.
func FetchFutureLargeBodySource(port, bodyLen int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";
function main(): i32 {
    let none: u8[] = [];
    let f: async.Future[u8[]] = fetch.fetch_future(%d, %d, "/big");
    let fs: async.Future[u8[]][] = [f];
    let bodies: u8[][] = async.gather(fs, none);
    if (bodies[0].len() == %d) { return 42; }
    return bodies[0].len() & 127;  // distinct small code on a truncated read
}`, LoopbackHostBE, port, bodyLen)
}

// AlphabetBody is n bytes cycling through the lowercase alphabet.
func AlphabetBody(n int) []byte {
	body := make([]byte, n)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	return body
}

// FetchAccumulatorSource is a program that reads one bodyLen-byte body
// from the loopback upstream at port through `fetch.send` and then through
// `fetch.fetch_future`, and pins how each accumulates it with the rc==1
// cliff counters: `send`'s uniquely owned buffer never copies a shared
// array, and the drain's captured chunk list copies pointers, three orders
// of magnitude below a byte-wise accumulator. Exit 42 when both hold.
func FetchAccumulatorSource(port, bodyLen int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";

function main(): i32 {
    match (fetch.send(fetch.get("http://127.0.0.1:%[1]d/"))) {
        Ok(resp) => { if (resp.body_bytes().len() != %[2]d) { return 1; } },
        Err(e) => { return 5; }
    }
    // send's buffer is a plain local, so it grows in place.
    if (__arr_push_shared_count() != 0) { return 2; }
    let h: i32 = fetch.ipv4(127, 0, 0, 1);
    let none: u8[] = [];
    let fs: async.Future[u8[]][] = [fetch.fetch_future(h, %[1]d, "/")];
    let bodies: u8[][] = async.gather(fs, none);
    if (bodies[0].len() != %[2]d) { return 3; }
    // A path that cannot stand on a request line resolves to the empty
    // body without connecting.
    let bad: async.Future[u8[]][] = [fetch.fetch_future(h, %[1]d, "/a\r\nX-Injected: 1")];
    let refused: u8[][] = async.gather(bad, none);
    if (refused[0].len() != 0) { return 6; }
    // __fetch_drain's list IS captured, so its appends cross the cliff —
    // pointer-sized, which is the whole point of carrying chunks.
    if (__arr_push_shared_bytes() > (8388608 as i64)) { return 4; }
    return 42;
}`, port, bodyLen)
}
