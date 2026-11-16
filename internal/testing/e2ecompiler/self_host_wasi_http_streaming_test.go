package e2ecompiler

import (
	"bufio"
	"io"
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

	// io.MultiReader hides the length, so these bodies arrive chunked with no Content-Length.
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
			resp, err := client.Post(base+"/len", "text/plain", io.MultiReader(strings.NewReader(c.body)))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != c.status || !strings.Contains(string(got), c.want) {
				t.Errorf("chunked %d bytes = %d %q; want %d containing %q", len(c.body), resp.StatusCode, got, c.status, c.want)
			}
		})
	}
}
