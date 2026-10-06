package e2eharness

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// StreamBodiesServerSource is a one-worker server with
// `stream_bodies` on (docs/NET-P3-SUSPENSION-PLAN.md §3.9): /sum reads
// its body a kilobyte at a time through the request's Stream and answers
// the byte count and sum; /text reads it whole with body_string, so a body
// that ended early is answered with the fault's status; /refuse answers
// 403 without reading the body; /peek reads the first kilobyte and
// answers, leaving the rest of the body unread; /ok is the hello. The body cap is 4 KiB
// and the minimum data rate 1000 B/s after a 300 ms grace, so the checks
// below can stall a body into a 408 and push a chunk past the cap into a
// 413 in well under a second.
func StreamBodiesServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/time";
function sum_body(req: HttpRequest): HttpResponse {
    let s: Stream = req.body_stream();
    let count: i32 = 0;
    let sum: i32 = 0;
    let going: boolean = true;
    while (going) {
        let (piece, next) = s.read_n(1024);
        s = next;
        if (piece.len() == 0) {
            going = false;
        }
        let i: i32 = 0;
        while (i < piece.len()) {
            sum = sum + piece[i] as i32;
            i = i + 1;
        }
        count = count + piece.len();
    }
    if (s.fault() != 0) {
        return http.text(s.fault(), "fault " + s.fault().to_string());
    }
    return http.ok("sum " + sum.to_string() + " len " + count.to_string());
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/sum") { return sum_body(req); }
    if (req.path == "/echo") { return http.bytes(200, req.body_bytes()); }
    if (req.path == "/text") {
        match (req.body_string()) {
            Ok(text) => { return http.ok("text " + text.len().to_string()); },
            Err(e) => { return e.to_response(); }
        }
    }
    if (req.path == "/refuse") { return http.text(403, "refused"); }
    if (req.path == "/peek") {
        let (head, rest) = req.body_stream().read_n(1024);
        return http.ok("peek " + head.len().to_string());
    }
    return http.ok("ok");
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), workers: 1, stream_bodies: true, min_data_rate: 1000, data_rate_grace: time.duration_millis(300), limits: http.HttpLimits { ...http.http_limits(), body: 4096 } };
    return serve.supervise(0, opts, handle);
}
`)
}

// StreamUploadBytes is the body the overlap check uploads: 3000 bytes,
// under the 4 KiB cap, each byte its index modulo 251, so the handler's
// sum is checkable.
func StreamUploadBytes() []byte {
	out := make([]byte, 3000)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

func streamUploadSum(body []byte) int {
	sum := 0
	for _, b := range body {
		sum += int(b)
	}
	return sum
}

// CheckStreamBodiesOverlap drives StreamBodiesServerSource as a client
// uploading slowly: the handler's pull parks on the body, so /ok on
// another connection is answered while the upload is still arriving, and
// the upload itself is answered with every byte once it has all arrived.
// Only a server whose handlers park passes this; the Go compiler's
// blocking fallback is checked by CheckStreamBodiesSequential.
func CheckStreamBodiesOverlap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	body := StreamUploadBytes()
	up := dialUpload(t, addr, "/sum", len(body))
	defer up.Close()
	if _, err := up.Write(body[:300]); err != nil {
		t.Fatal(err)
	}
	// Give the worker time to read the head and park the handler on the
	// body before the second connection arrives.
	time.Sleep(20 * time.Millisecond)
	started := time.Now()
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside an upload in progress: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("/ok beside an upload took %v: the worker stalled on the body", waited)
	}
	for at := 300; at < len(body); at += 300 {
		end := at + 300
		if end > len(body) {
			end = len(body)
		}
		if _, err := up.Write(body[at:end]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	want := fmt.Sprintf("sum %d len %d", streamUploadSum(body), len(body))
	if resp := readWhole(t, up); !strings.Contains(resp, "HTTP/1.1 200") || !strings.Contains(resp, want) {
		t.Fatalf("the upload's response: want 200 with %q, got\n%s", want, resp)
	}
}

// CheckStreamBodiesSequential drives StreamBodiesServerSource one client
// at a time, the checks that hold whether or not the handler parks: a
// chunked upload with a request pipelined behind it is answered in order
// with the next request's bytes taken back from the pull; a body that
// stalls under the minimum data rate is answered 408 and the connection
// closed; a chunk past the body cap is answered 413; an `Expect:
// 100-continue` upload is invited once the handler reads, and a handler
// that refuses never invites it and the connection closes with the body
// unread; a client that goes away mid-body leaves the worker serving.
func CheckStreamBodiesSequential(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	checkStreamBodiesBinary(t, addr)
	// A chunked body in two chunks with a trailer, and a /ok pipelined
	// behind it in the same write.
	conn := dialRaw(t, addr)
	chunked := "POST /text HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\nX-Sum: 11\r\n\r\n" +
		"GET /ok HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, chunked); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	first := readResponseFrom(t, reader, "chunked /text")
	if first.StatusCode != 200 || !strings.Contains(bodyText(t, first), "text 11") {
		t.Fatalf("chunked /text: want 200 text 11, got %d %q", first.StatusCode, bodyText(t, first))
	}
	second := readResponseFrom(t, reader, "/ok behind the chunked body")
	if second.StatusCode != 200 || !strings.Contains(bodyText(t, second), "ok") {
		t.Fatalf("/ok pipelined behind a chunked body: want 200 ok, got %d %q", second.StatusCode, bodyText(t, second))
	}
	conn.Close()

	// A body that stalls: 100 of 3000 bytes, then silence past the grace
	// (300 ms) and what 100 bytes buy at 1000 B/s (100 ms).
	stalled := dialUpload(t, addr, "/sum", 3000)
	defer stalled.Close()
	if _, err := stalled.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if resp := readWhole(t, stalled); !strings.Contains(resp, "HTTP/1.1 408") {
		t.Fatalf("a stalled body: want 408, got\n%s", resp)
	}

	// A chunk past the 4 KiB cap is refused at its size line.
	big := dialRaw(t, addr)
	defer big.Close()
	if _, err := io.WriteString(big, "POST /sum HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n1400\r\n"); err != nil {
		t.Fatal(err)
	}
	if resp := readWhole(t, big); !strings.Contains(resp, "HTTP/1.1 413") {
		t.Fatalf("a chunk past the cap: want 413, got\n%s", resp)
	}

	// Expect: 100-continue, read by the handler: the interim response
	// comes first, then the body is sent and answered.
	expecting := dialRaw(t, addr)
	defer expecting.Close()
	if _, err := io.WriteString(expecting, "POST /text HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	ereader := bufio.NewReader(expecting)
	interim := readResponseFrom(t, ereader, "100 Continue")
	if interim.StatusCode != 100 {
		t.Fatalf("Expect: 100-continue: want 100 first, got %d", interim.StatusCode)
	}
	if _, err := io.WriteString(expecting, "hello"); err != nil {
		t.Fatal(err)
	}
	final := readResponseFrom(t, ereader, "/text after 100 Continue")
	if final.StatusCode != 200 || !strings.Contains(bodyText(t, final), "text 5") {
		t.Fatalf("/text after 100 Continue: want 200 text 5, got %d %q", final.StatusCode, bodyText(t, final))
	}

	// A handler that refuses without reading never invites the body: no
	// 100, the 403 at once, and the connection closed since the body was
	// not read.
	refused := dialRaw(t, addr)
	defer refused.Close()
	if _, err := io.WriteString(refused, "POST /refuse HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp := readWhole(t, refused)
	if strings.Contains(resp, "HTTP/1.1 100") || !strings.Contains(resp, "HTTP/1.1 403") {
		t.Fatalf("a refusing handler under Expect: want 403 alone and the close, got\n%s", resp)
	}

	// A handler that stops reading early without a fault, the rest of the
	// body still on the wire: its response goes out and the connection
	// closes, so the bytes that follow are never read as a request.
	peek := dialRaw(t, addr)
	defer peek.Close()
	peekBody := StreamUploadBytes()
	if _, err := io.WriteString(peek, fmt.Sprintf("POST /peek HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\n\r\n", len(peekBody))); err != nil {
		t.Fatal(err)
	}
	if _, err := peek.Write(peekBody[:1500]); err != nil {
		t.Fatal(err)
	}
	preader := bufio.NewReader(peek)
	peeked := readResponseFrom(t, preader, "/peek on a partly sent body")
	if peeked.StatusCode != 200 || !strings.Contains(bodyText(t, peeked), "peek 1024") {
		t.Fatalf("a handler that read part of the body: want 200 peek 1024, got %d %q", peeked.StatusCode, bodyText(t, peeked))
	}
	// The rest of the body and a request behind it: the connection is
	// closed, so no second response comes back.
	_, _ = peek.Write(peekBody[1500:])
	_, _ = io.WriteString(peek, "GET /ok HTTP/1.1\r\nHost: h\r\n\r\n")
	if after, _ := io.ReadAll(preader); strings.Contains(string(after), "HTTP/1.1 ") {
		t.Fatalf("the body left unread was answered as a request:\n%s", after)
	}

	// A client that half-closes mid-body is answered 400, whether its head
	// started a handler before the end of stream arrived or arrived with it.
	short := dialUpload(t, addr, "/sum", 3000)
	defer short.Close()
	if _, err := short.Write(make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
	if err := short.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if resp := readWhole(t, short); !strings.Contains(resp, "HTTP/1.1 400") {
		t.Fatalf("a body cut short by the client: want 400, got\n%s", resp)
	}

	// A client that goes away mid-body: the worker keeps serving.
	gone := dialUpload(t, addr, "/sum", 3000)
	if _, err := gone.Write(make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
	gone.Close()
	time.Sleep(50 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok after a client left mid-body: want 200, got\n%s", resp)
	}
}

// Arbitrary bytes survive a suspended pull and a following request in the
// same read. The echo compares every byte, including malformed UTF-8 and NUL.
func checkStreamBodiesBinary(t *testing.T, addr string) {
	t.Helper()
	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i)
	}
	nextBody := make([]byte, 777)
	for i := range nextBody {
		nextBody[i] = byte(255 - i%256)
	}
	for _, tc := range []struct {
		framing string
		invite  bool
	}{
		{"content-length", false}, {"content-length", true},
		{"chunked", false}, {"chunked", true},
	} {
		t.Run(fmt.Sprintf("binary-%s-invite-%t", tc.framing, tc.invite), func(t *testing.T) {
			conn := dialRaw(t, addr)
			defer conn.Close()
			header := fmt.Sprintf("Content-Length: %d\r\n", len(body))
			if tc.framing == "chunked" {
				header = "Transfer-Encoding: chunked\r\n"
			}
			head := "POST /echo HTTP/1.1\r\nHost: h\r\n" + header
			reader := bufio.NewReader(conn)
			var first, tail bytes.Buffer
			if tc.invite {
				if _, err := io.WriteString(conn, head+"Expect: 100-continue\r\n\r\n"); err != nil {
					t.Fatal(err)
				}
				interim := readResponseFrom(t, reader, "binary upload invitation")
				if interim.StatusCode != 100 {
					t.Fatalf("want 100 before sending the binary body, got %d", interim.StatusCode)
				}
			} else {
				// Header and partial body together exercise the pending buffer.
				first.WriteString(head + "\r\n")
			}
			if tc.framing == "chunked" {
				fmt.Fprintf(&first, "%x\r\n", len(body))
			}
			first.Write(body[:131])
			tail.Write(body[131:])
			if tc.framing == "chunked" {
				tail.WriteString("\r\n0\r\nX-End: yes\r\n\r\n")
			}
			fmt.Fprintf(&tail, "POST /echo HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(nextBody))
			tail.Write(nextBody)
			if _, err := conn.Write(first.Bytes()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
			if _, err := conn.Write(tail.Bytes()); err != nil {
				t.Fatal(err)
			}
			for i, want := range [][]byte{body, nextBody} {
				resp := readResponseFrom(t, reader, fmt.Sprintf("binary response %d", i))
				got, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || !bytes.Equal(got, want) {
					t.Fatalf("binary response %d: status=%d bytes=%d, want 200 and %d exact bytes; error=%v", i, resp.StatusCode, len(got), len(want), err)
				}
			}
		})
	}
	t.Run("binary-text-rejected", func(t *testing.T) {
		conn := dialUpload(t, addr, "/text", len(body))
		defer conn.Close()
		if _, err := conn.Write(body); err != nil {
			t.Fatal(err)
		}
		resp := readResponseFrom(t, bufio.NewReader(conn), "invalid UTF-8 text body")
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("invalid UTF-8 text body: want 400, got %d", resp.StatusCode)
		}
	})
}

// dialUpload opens a connection and writes the head of a POST to `path`
// declaring `length` body bytes, for the caller to send at its pace.
func dialUpload(t *testing.T, addr, path string, length int) net.Conn {
	t.Helper()
	c := dialRaw(t, addr)
	head := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", path, length)
	if _, err := io.WriteString(c, head); err != nil {
		c.Close()
		t.Fatal(err)
	}
	return c
}

func dialRaw(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		c.Close()
		t.Fatal(err)
	}
	return c
}

// readWhole reads a connection to its close.
func readWhole(t *testing.T, c net.Conn) string {
	t.Helper()
	b, _ := io.ReadAll(c)
	return string(b)
}

func bodyText(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading a response body: %v", err)
	}
	return string(b)
}
