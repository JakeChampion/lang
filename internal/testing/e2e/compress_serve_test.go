package e2e

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/http/compress under std/serve's loop, its gzip and zstd read by
// Go's decoders: a page of numbered lines and one mixing them with
// pseudo-random words, each asked for in gzip, in zstd and without
// `Accept-Encoding`. The interpreter and the x86-64 backend.
func TestCompressServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeCompressServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkCompressServe(t, addr)
}

func TestCompressServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeCompressServeSource(t)
	out := filepath.Join(filepath.Dir(src), "compress_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkCompressServe(t, addr)
}

func writeCompressServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "compress_serve.fern")
	if err := os.WriteFile(src, []byte(compressServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const compressServeSource = `import "std/http";
import "std/http/compress";
import "std/platform";
import "std/serve";
import "std/utf8";

function lines(): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < 3000) {
    out = out + "line " + i.to_string() + " of the corpus\n";
    i = i + 1;
  }
  return out;
}

function mixed(): string {
  let out: string = "";
  let x: i32 = 7;
  let i: i32 = 0;
  while (i < 2000) {
    let word: u8[] = [];
    let k: i32 = 0;
    while (k < 8) {
      x = x * 1103515245 + 12345 & 2147483647;
      word = word.append((97 + (x >> 16) % 26) as u8);
      k = k + 1;
    }
    out = out + "row " + i.to_string() + ": " + utf8.from_bytes_lossy(word) + " and the same tail every time\n";
    i = i + 1;
  }
  return out;
}

function page(req: HttpRequest): HttpResponse {
  if (req.path == "/mixed") {
    return http.ok(mixed()).with_content_type("text/plain");
  }
  return http.ok(lines()).with_content_type("text/plain");
}

function handle(req: HttpRequest, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  use req <- compress.compressed(req);
  return Ok(page(req));
}

function main(): i32 {
  return serve.run(0, serve.config(), handle);
}
`

func compressExpected(path string) string {
	var b strings.Builder
	if path == "/mixed" {
		x := int32(7)
		for i := 0; i < 2000; i++ {
			word := make([]byte, 8)
			for k := range word {
				x = (x*1103515245 + 12345) & 2147483647
				word[k] = byte(97 + (x>>16)%26)
			}
			fmt.Fprintf(&b, "row %d: %s and the same tail every time\n", i, word)
		}
		return b.String()
	}
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "line %d of the corpus\n", i)
	}
	return b.String()
}

func checkCompressServe(t *testing.T, addr string) {
	t.Helper()
	tr := &http.Transport{DisableCompression: true}
	client := &http.Client{Transport: tr}
	for _, path := range []string{"/lines", "/mixed"} {
		want := compressExpected(path)
		for _, accept := range []string{"gzip", "zstd", ""} {
			req, err := http.NewRequest("GET", "http://"+addr+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if accept != "" {
				req.Header.Set("Accept-Encoding", accept)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %q: %v", path, accept, err)
			}
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("%s %q: Vary %q", path, accept, got)
			}
			if got := resp.Header.Get("Content-Encoding"); got != accept {
				t.Fatalf("%s: Content-Encoding %q, want %q", path, got, accept)
			}
			body := decodeCompressed(t, path, accept, raw, len(want))
			if accept != "" && len(raw)*4 > len(want) {
				t.Errorf("%s %s: %d bytes coded from %d", path, accept, len(raw), len(want))
			}
			if string(body) != want {
				t.Errorf("%s %q: body differs (%d bytes, want %d)", path, accept, len(body), len(want))
			}
		}
	}
}

// decodeCompressed is `raw` read by Go's decoder for `coding`.
func decodeCompressed(t *testing.T, path, coding string, raw []byte, size int) []byte {
	t.Helper()
	switch coding {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("%s: Go's gzip reader: %v", path, err)
		}
		return body
	case "zstd":
		body, err := zstdDecodeGo(raw, size)
		if err != nil {
			t.Fatalf("%s: Go's zstd decoder: %v", path, err)
		}
		return body
	}
	return raw
}
