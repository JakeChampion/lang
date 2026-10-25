package e2eharness

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// StartBodyUpstream serves body to every request whose Host names the
// listener, and a 400 to any other.
func StartBodyUpstream(t *testing.T, body []byte) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host := ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil || req.Host != host {
					fmt.Fprint(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 8\r\n\r\nbad host")
					return
				}
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TaskFetchFanoutSource(port int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";

function body_of(url: string): string {
    match (fetch.send(fetch.get(url))) {
        Ok(resp) => { return resp.body_string(); },
        Err(e) => { return e.message(); }
    }
    return "";
}

function main(): i32 {
    let up: string = "http://127.0.0.1:%d";
    let both: (() => string)[] = [() => body_of(up + "/1"), () => body_of(up + "/2")];
    let bodies: string[] = async.gather_tasks(both, "incomplete");
    if (bodies[0] == "hello-world" && bodies[1] == "hello-world") { return 42; }
    return 85;
}`, port)
}

func TaskFetchLargeBodySource(port, bodyLen int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";

function size_of(url: string): i32 {
    match (fetch.send(fetch.get(url))) {
        Ok(resp) => { return resp.body_bytes().len(); },
        Err(e) => { return 0 - 1; }
    }
    return 0 - 1;
}

function main(): i32 {
    let fetches: (() => i32)[] = [() => size_of("http://127.0.0.1:%d/big")];
    let sizes: i32[] = async.gather_tasks(fetches, 0 - 2);
    if (sizes[0] == %d) { return 42; }
    return sizes[0] & 127;  // distinct small code on a truncated read
}`, port, bodyLen)
}

func AlphabetBody(n int) []byte {
	body := make([]byte, n)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	return body
}

func FetchAccumulatorSource(port, bodyLen int) string {
	return fmt.Sprintf(`import "std/fetch";

function main(): i32 {
    match (fetch.send(fetch.get("http://127.0.0.1:%[1]d/"))) {
        Ok(resp) => { if (resp.body_bytes().len() != %[2]d) { return 1; } },
        Err(e) => { return 5; }
    }
    // send's buffer is a plain local, so it grows in place.
    if (__arr_push_shared_count() != 0) { return 2; }
    return 42;
}`, port, bodyLen)
}
