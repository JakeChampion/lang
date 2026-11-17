package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const wasiHttpStreamingSrc = `
import "std/headers";
import "std/http";
import "std/stream";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/echo") {
        match (req.body.source) {
            Some(src) => {
                let next = src.next;
                return http.chunks_with_trailers(200, (i: i32): Option[u8[]] => next(), (): HeaderMap => headers.new().append("x-end", "done"))
                    .with_header("trailer", "x-end")
                    .with_trailer("x-start", "early");
            },
            None => { return http.text(500, "the request body has no source"); }
        }
    }
    if (req.path == "/file") { return http.file("/etc/hosts"); }
    match (req.body_text()) {
        Ok(t) => { return http.ok("len=" + t.len().to_string()); },
        Err(e) => { return e.to_response(); }
    }
}
`

// The proxy hands the handler a lazy request body and writes each response chunk as it is
// produced: the echo below answers each line before the client sends the next one.
func TestSelfHostWasiHttpStreamsBodies(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	t.Setenv("FERN_STRICT_IR", "1")
	component := compileWasiHttp(t, t.TempDir(), wasiHttpStreamingSrc, "streaming.wasm")
	addr, slog, stop := startServe(t, wasmtime, component)
	defer stop()
	client := &http.Client{Timeout: 10 * time.Second}
	base := "http://" + addr

	var file *http.Response
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if file, err = client.Get(base + "/file"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /file: %v\nwasmtime serve:\n%s", err, slog.String())
		}
	}
	body, _ := io.ReadAll(file.Body)
	file.Body.Close()
	if file.StatusCode != 500 {
		t.Errorf("a file body on the proxy = %d %q; want 500, as the proxy world has no filesystem", file.StatusCode, body)
	}

	t.Run("duplex", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pw.Close()
		req, err := http.NewRequest("POST", base+"/echo", pr)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("TE", "trailers")
		go pw.Write([]byte("ping-1\n"))
		answered := make(chan *http.Response, 1)
		failed := make(chan error, 1)
		go func() {
			resp, err := client.Do(req)
			if err != nil {
				failed <- err
				return
			}
			answered <- resp
		}()
		var resp *http.Response
		select {
		case resp = <-answered:
		case err := <-failed:
			t.Fatalf("POST /echo: %v\nwasmtime serve:\n%s", err, slog.String())
		case <-time.After(5 * time.Second):
			t.Fatalf("no response head while the request body is open\nwasmtime serve:\n%s", slog.String())
		}
		defer resp.Body.Close()
		lines := bufio.NewReader(resp.Body)
		for _, line := range []string{"ping-1\n", "ping-2\n"} {
			if line == "ping-2\n" {
				if _, err := pw.Write([]byte(line)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := lines.ReadString('\n')
			if err != nil || got != line {
				t.Fatalf("echoed %q, %v; want %q", got, err, line)
			}
		}
		pw.Close()
		if rest, err := io.ReadAll(lines); err != nil || len(rest) != 0 {
			t.Fatalf("after the request body ended: %q, %v; want the end of the body", rest, err)
		}
		if got := resp.Trailer.Get("x-start"); got != "early" {
			t.Errorf("response trailer x-start = %q; want %q", got, "early")
		}
		if got := resp.Trailer.Get("x-end"); got != "done" {
			t.Errorf("producer trailer x-end = %q; want %q", got, "done")
		}
	})

	// These bodies arrive chunked with no Content-Length. The server answers a body past
	// the limit and closes while the client still writes, so the client reads the answer
	// on its own connection and does not stop at a write error.
	for _, c := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"small", "hello", 200, "len=5"},
		{"past-limit", strings.Repeat("b", 2<<20), 413, "the body is past the limit"},
	} {
		t.Run("chunked-"+c.name, func(t *testing.T) {
			resp, err := postChunked(addr, "/len", c.body)
			if err != nil {
				t.Fatalf("%v\nwasmtime serve:\n%s", err, slog.String())
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.status || !strings.Contains(string(got), c.want) {
				t.Errorf("chunked %d bytes = %d %q; want %d containing %q", len(c.body), resp.StatusCode, got, c.status, c.want)
			}
		})
	}
}

// postChunked sends body to path in chunks of 32 KiB and gives the response.
// The write runs apart from the read, and its error is not the result: a
// server can answer and close before it reads the whole body.
func postChunked(addr, path, body string) (*http.Response, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	go func() {
		w := bufio.NewWriter(conn)
		fmt.Fprintf(w, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n", path, addr)
		for rest := body; len(rest) > 0; {
			n := min(len(rest), 32<<10)
			fmt.Fprintf(w, "%x\r\n%s\r\n", n, rest[:n])
			rest = rest[n:]
		}
		w.WriteString("0\r\n\r\n")
		w.Flush()
	}()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	resp.Body = closeBoth{resp.Body, conn}
	return resp, nil
}

// closeBoth closes the connection of a response with its body.
type closeBoth struct {
	io.ReadCloser
	conn net.Conn
}

func (c closeBoth) Close() error {
	c.ReadCloser.Close()
	return c.conn.Close()
}
