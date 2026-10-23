package e2e

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// File responses must use the bounded producer, not Body.bytes (which
// deliberately does not read files). The native file exceeds three times
// the HTTP/3 producer's unacknowledged-byte cap; hashes check every byte.
func TestServeH3FileX86_64(t *testing.T) {
	checkServeH3Files(t, "x86-64-linux", 3*262144+123)
}

func TestServeH3FileInterp(t *testing.T) {
	checkServeH3Files(t, "interp", 8193)
}

func TestServeH3FileArm64(t *testing.T) {
	checkServeH3Files(t, "arm64-linux", 8193)
}

func checkServeH3Files(t *testing.T, target string, size int) {
	t.Helper()
	config := "serve.config()"
	if target == "interp" {
		// Match the existing positive interpreted-TLS fixture. Native
		// servers and all production/negative deadline gates keep defaults.
		config = "serve.Config { ...serve.config(), recv_deadline: time.duration_seconds(60 as i64) }"
	}
	w := newServeH3WorldWithConfig(t, config)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((17*i + 3) % 251)
	}
	path := filepath.Join(w.dir, "binary.dat")
	empty := filepath.Join(w.dir, "empty.dat")
	for name, body := range map[string][]byte{path: data, empty: {}} {
		if err := os.WriteFile(name, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	from, length, tail := 13, size/3, size-17
	ranged := append([]byte("<a>"), data[from:from+length]...)
	ranged = append(ranged, []byte("<b>")...)
	ranged = append(ranged, data[tail:tail+11]...)
	ranged = append(ranged, []byte("<z>")...)
	handler := fmt.Sprintf(`function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/missing") {
    return http.file(%q);
  }
  if (req.path == "/empty") {
    return http.file(%q);
  }
  let resp: HttpResponse = http.file(%q);
  if (req.path == "/ranges") {
    return HttpResponse { ...resp, status: 206, body: BodyFileRanges(%q, [13 as i64, %d as i64, %d as i64, 11 as i64], ["<a>".bytes(), "<b>".bytes(), "<z>".bytes()]) };
  }
  if (req.path == "/bad-ranges") {
    return HttpResponse { ...resp, body: BodyFileRanges(%q, [0 as i64, 0 - 1 as i64], [[], []]) };
  }
  if (req.path == "/outside") {
    return HttpResponse { ...resp, body: BodyFileRanges(%q, [%d as i64, 2 as i64], [[], []]) };
  }
  if (req.path == "/no-content") {
    return HttpResponse { ...resp, status: 204 };
  }
  if (req.path == "/not-modified") {
    return HttpResponse { ...resp, status: 304 };
  }
  return HttpResponse { ...resp, trailers: headers.header_map_new().append("x-file", "done") };
}

`, path+".absent", empty, path, path, length, tail, path, path, size-1)
	source := replaceH3FileSpan(t, serveH3Source, "function handle(", "function main():", handler)
	source = strings.NewReplacer("@CERT@", filepath.Join(w.dir, "cert.pem"), "@KEY@", filepath.Join(w.dir, "key.pem"), "@CONFIG@", config).Replace(source)
	if err := os.WriteFile(w.src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	client := strings.Replace(serveH3ClientSource, "import \"std/net\";", "import \"std/net\";\nimport \"std/crypto\";", 1)
	client = replaceH3FileSpan(t, client, "function summary(", "struct Asked", `function summary(method: string, resp: HttpResponse): string {
  let body: u8[] = resp.body_bytes();
  return resp.status.to_string() + " bytes=" + body.len().to_string() + " sha256=" + crypto.sha256_new().update(body).final_hex() + " length=" + resp.headers.get("content-length").unwrap_or("-") + " trailer=" + resp.trailers.get("x-file").unwrap_or("-");
}

`)
	client = replaceH3FileSpan(t, client, "  let reqs: HttpRequest[] =", "  let c: h3q.Conn =", `  let reqs: HttpRequest[] = [request("GET", "/file", []), request("HEAD", "/file", []), request("GET", "/ranges", []), request("HEAD", "/ranges", []), request("GET", "/missing", []), request("GET", "/bad-ranges", []), request("GET", "/outside", []), request("GET", "/empty", []), request("GET", "/no-content", []), request("GET", "/not-modified", [])];
  let a: Asked = Asked { reqs: reqs, ids: [], answers: ["", "", "", "", "", "", "", "", "", ""], got: 0, sent: false };
`)
	clientSrc := filepath.Join(w.dir, "file_client.fern")
	if err := os.WriteFile(clientSrc, []byte(client), 0o644); err != nil {
		t.Fatal(err)
	}
	w.client = filepath.Join(w.dir, "file_client.bin")
	if out, err := exec.Command(w.fern, "-target", "x86-64-linux", "-o", w.client, clientSrc).CombinedOutput(); err != nil {
		t.Fatalf("file client build: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if target == "interp" {
		cmd = exec.Command(buildLangBinForInterp(t), "-interp", w.src)
	} else {
		bin := filepath.Join(w.dir, "file_server.bin")
		if out, err := exec.Command(w.fern, "-target", target, "-o", bin, w.src).CombinedOutput(); err != nil {
			t.Fatalf("file server build: %v\n%s", err, out)
		}
		if target == "arm64-linux" {
			runner, ok := arm64Runner()
			if !ok {
				if envflag.On("FERN_REQUIRE_ARM64_TOOLING") {
					t.Fatal("ARM64 runner required for HTTP/3 file-serving coverage")
				}
				t.Skip("no ARM64 runner for HTTP/3 file-serving coverage")
			}
			if runner == "" {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner, bin)
			}
		} else {
			cmd = runX86Bin(w.qemu, bin)
		}
	}
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runX86Bin(w.qemu, w.client, port, w.ca, "0", "0").CombinedOutput()
	if err != nil {
		t.Fatalf("file client: %v\n%s", err, out)
	}
	line := func(method, path string, status int, body []byte, length, trailer string) string {
		return fmt.Sprintf("%s %s -> %d bytes=%d sha256=%x length=%s trailer=%s\n", method, path, status, len(body), sha256.Sum256(body), length, trailer)
	}
	want := line("GET", "/file", 200, data, fmt.Sprint(size), "done") +
		line("HEAD", "/file", 200, nil, fmt.Sprint(size), "-") +
		line("GET", "/ranges", 206, ranged, fmt.Sprint(len(ranged)), "-") +
		line("HEAD", "/ranges", 206, nil, fmt.Sprint(len(ranged)), "-") +
		line("GET", "/missing", 404, []byte("Not Found"), "9", "-") +
		line("GET", "/bad-ranges", 500, nil, "0", "-") +
		line("GET", "/outside", 404, []byte("Not Found"), "9", "-") +
		line("GET", "/empty", 200, nil, "0", "-") +
		line("GET", "/no-content", 204, nil, "-", "-") +
		line("GET", "/not-modified", 304, nil, "-", "-")
	if string(out) != want {
		t.Errorf("HTTP/3 file responses:\n%s\nwant:\n%s", out, want)
	}
}

func replaceH3FileSpan(t *testing.T, source, begin, end, replacement string) string {
	t.Helper()
	if strings.Count(source, begin) != 1 || strings.Count(source, end) != 1 {
		t.Fatalf("HTTP/3 fixture shape changed around %q / %q", begin, end)
	}
	start, stop := strings.Index(source, begin), strings.Index(source, end)
	if stop <= start {
		t.Fatal("HTTP/3 fixture markers are out of order")
	}
	return source[:start] + replacement + source[stop:]
}
