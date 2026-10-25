package e2e

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestStaticServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src, big := writeStaticServeSource(t, "serve.config()")
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkStaticServe(t, addr, big, &http.Transport{}, 1)
	checkStaticPaths(t, addr, &http.Transport{}, 1)
}

func TestStaticServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src, big := writeStaticServeSource(t, "serve.config()")
	out := filepath.Join(filepath.Dir(src), "static_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkStaticServe(t, addr, big, &http.Transport{}, 1)
	checkStaticPaths(t, addr, &http.Transport{}, 1)
}

func writeStaticServeSource(t *testing.T, config string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "www")
	big := make([]byte, 300_000)
	for i := range big {
		big[i] = byte(i*7 + i/251)
	}
	files := map[string][]byte{
		"a.txt":           []byte("hello world"),
		"a.txt.gz":        []byte("GZ-BYTES"),
		"big.bin":         big,
		"docs/index.html": []byte("<h1>docs</h1>"),
		".env":            []byte("SECRET=1"),
		"100%.txt":        []byte("percent"),
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(dir, "static_serve.fern")
	prog := strings.ReplaceAll(strings.ReplaceAll(staticServeSource, "@ROOT@", root), "@CONFIG@", config)
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src, big
}

const staticServeSource = `import "std/http";
import "std/http/static";
import "std/platform";
import "std/serve";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/file/whole") {
    return http.file("@ROOT@/big.bin");
  }
  if (req.path == "/file/gone") {
    return http.file("@ROOT@/gone.bin");
  }
  return static.serve(req, static.dir("@ROOT@"), plat);
}

function main(): i32 {
  return serve.exit_code(serve.run(0, @CONFIG@, handle));
}
`

func checkStaticServe(t *testing.T, addr string, big []byte, tr http.RoundTripper, major int) {
	t.Helper()
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path string, fields ...string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept-Encoding", "identity")
		for i := 0; i+1 < len(fields); i += 2 {
			req.Header.Set(fields[i], fields[i+1])
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s %s: reading the body: %v", method, path, err)
		}
		if resp.ProtoMajor != major {
			t.Fatalf("%s %s: HTTP/%d, want HTTP/%d", method, path, resp.ProtoMajor, major)
		}
		return resp, body
	}
	expect := func(what string, resp *http.Response, body []byte, status int, want []byte) {
		t.Helper()
		if resp.StatusCode != status {
			t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
		}
		if want != nil && !bytes.Equal(body, want) {
			t.Errorf("%s: %d bytes, want %d (first %q)", what, len(body), len(want), body[:min(len(body), 16)])
		}
	}

	resp, body := do("GET", "/big.bin")
	expect("whole", resp, body, 200, big)
	if resp.ContentLength != int64(len(big)) {
		t.Errorf("whole: Content-Length %d, want %d", resp.ContentLength, len(big))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" || resp.Header.Get("Accept-Ranges") != "bytes" || resp.Header.Get("Last-Modified") == "" {
		t.Errorf("whole: validators %q / %q / %q", etag, resp.Header.Get("Accept-Ranges"), resp.Header.Get("Last-Modified"))
	}

	resp, body = do("GET", "/big.bin", "Range", "bytes=100000-250000")
	expect("range", resp, body, 206, big[100000:250001])
	if got := resp.Header.Get("Content-Range"); got != "bytes 100000-250000/300000" || resp.ContentLength != 150001 {
		t.Errorf("range: Content-Range %q, Content-Length %d", got, resp.ContentLength)
	}
	resp, body = do("GET", "/big.bin", "Range", "bytes=-10")
	expect("suffix", resp, body, 206, big[len(big)-10:])
	resp, body = do("GET", "/big.bin", "Range", "bytes=10-20, 100000-250000, -10")
	expect("multipart", resp, nil, 206, nil)
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("multipart: Content-Length %d, %d bytes", resp.ContentLength, len(body))
	}
	want := []string{"bytes 10-20/300000", "bytes 100000-250000/300000", "bytes 299990-299999/300000"}
	wantBytes := [][]byte{big[10:21], big[100000:250001], big[299990:]}
	parts := byteranges(t, resp.Header, body)
	if len(parts) != len(want) {
		t.Fatalf("multipart: %d parts, want %d (Content-Type %q)", len(parts), len(want), resp.Header.Get("Content-Type"))
	}
	for i, p := range parts {
		if p.contentRange != want[i] || p.contentType != "application/octet-stream" || !bytes.Equal(p.body, wantBytes[i]) {
			t.Errorf("multipart part %d: %q %q, %d bytes; want %q, %d bytes", i, p.contentRange, p.contentType, len(p.body), want[i], len(wantBytes[i]))
		}
	}
	resp, _ = do("GET", "/big.bin", "Range", "bytes=300000-")
	expect("unsatisfiable", resp, nil, 416, nil)
	if got := resp.Header.Get("Content-Range"); got != "bytes */300000" {
		t.Errorf("unsatisfiable: Content-Range %q", got)
	}
	resp, body = do("GET", "/big.bin", "If-None-Match", etag)
	expect("not modified", resp, body, 304, []byte{})
	resp, body = do("HEAD", "/big.bin")
	expect("head", resp, body, 200, []byte{})
	if resp.ContentLength != int64(len(big)) {
		t.Errorf("head: Content-Length %d, want %d", resp.ContentLength, len(big))
	}

	resp, body = do("GET", "/file/whole")
	expect("http.file", resp, body, 200, big)
	if resp.ContentLength != int64(len(big)) {
		t.Errorf("http.file: Content-Length %d, want %d", resp.ContentLength, len(big))
	}
	resp, body = do("HEAD", "/file/whole")
	expect("http.file head", resp, body, 200, []byte{})
	if resp.ContentLength != int64(len(big)) {
		t.Errorf("http.file head: Content-Length %d, want %d", resp.ContentLength, len(big))
	}
	resp, _ = do("GET", "/file/gone")
	expect("http.file gone", resp, nil, 404, nil)

	resp, body = do("GET", "/a.txt", "Accept-Encoding", "gzip")
	expect("precompressed", resp, body, 200, []byte("GZ-BYTES"))
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("precompressed: Content-Encoding %q, Vary %q", resp.Header.Get("Content-Encoding"), resp.Header.Get("Vary"))
	}
	resp, body = do("GET", "/a.txt")
	expect("identity", resp, body, 200, []byte("hello world"))

	resp, _ = do("GET", "/docs")
	expect("redirect", resp, nil, 301, nil)
	if got := resp.Header.Get("Location"); got != "docs/" {
		t.Errorf("redirect: Location %q", got)
	}
	resp, body = do("GET", "/docs/")
	expect("index", resp, body, 200, []byte("<h1>docs</h1>"))
	for _, p := range []string{"/.env", "/missing", "/docs/%2e%2e/.env"} {
		resp, _ = do("GET", p)
		expect(p, resp, nil, 404, nil)
	}
}

func checkStaticPaths(t *testing.T, addr string, tr *http.Transport, major int) {
	t.Helper()
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	get := func(path string) (int, []byte) {
		t.Helper()
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.ProtoMajor != major {
			t.Errorf("GET %s: HTTP/%d, want HTTP/%d", path, resp.ProtoMajor, major)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("GET %s: reading the body: %v", path, err)
		}
		return resp.StatusCode, body
	}
	if status, body := get("/100%25.txt"); status != 200 || string(body) != "percent" {
		t.Errorf("decoded once: %d %q, want 200 \"percent\"", status, body)
	}
	for _, p := range []string{"/%2e%2e/static_serve.fern", "/docs/..%2f..%2fstatic_serve.fern"} {
		if status, _ := get(p); status != 400 {
			t.Errorf("%s: status %d, want 400", p, status)
		}
	}
}

type byterangePart struct {
	contentType, contentRange string
	body                      []byte
}

func byteranges(t *testing.T, h http.Header, body []byte) []byterangePart {
	t.Helper()
	mt, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mt != "multipart/byteranges" {
		return nil
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []byterangePart
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Errorf("multipart/byteranges: %v", err)
			return parts
		}
		b, err := io.ReadAll(p)
		if err != nil {
			t.Errorf("multipart/byteranges part: %v", err)
		}
		parts = append(parts, byterangePart{p.Header.Get("Content-Type"), p.Header.Get("Content-Range"), b})
	}
}

func contentShape(t *testing.T, h http.Header, body []byte) string {
	t.Helper()
	parts := byteranges(t, h, body)
	if parts == nil {
		return h.Get("Content-Type") + " | " + string(body)
	}
	out := "multipart/byteranges"
	for _, p := range parts {
		out += fmt.Sprintf(" | %s %s %q", p.contentType, p.contentRange, p.body)
	}
	return out
}

func TestStaticPreconditionsMatchGo(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src, _ := writeStaticServeSource(t, "serve.config()")
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	file := filepath.Join(filepath.Dir(src), "www", "a.txt")
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("\"%d.%d-%d\"", st.ModTime().Unix(), st.ModTime().Nanosecond(), st.Size())
	oracle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(file)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		w.Header().Set("ETag", tag)
		http.ServeContent(w, r, "a.txt", st.ModTime(), f)
	}))
	defer oracle.Close()

	mtime := st.ModTime().UTC()
	date := func(d time.Duration) string { return mtime.Add(d).Format(http.TimeFormat) }
	ifMatch := []string{"", tag, "\"other\"", "*", "W/" + tag, "\"other\", " + tag}
	ifUnmodified := []string{"", date(0), date(-time.Hour), date(time.Hour), "garbage"}
	ifNoneMatch := []string{"", tag, "\"other\"", "*", "W/" + tag}
	ifModified := []string{"", date(0), date(-time.Hour), date(time.Hour)}
	ranges := [][2]string{{"", ""}, {"bytes=0-4", ""}, {"bytes=-3", ""}, {"bytes=6-", ""}, {"bytes=11-", ""},
		{"bytes=0-4", tag}, {"bytes=0-4", "\"other\""}, {"bytes=0-4", "W/" + tag}, {"bytes=0-4", date(0)}, {"bytes=0-4", date(-time.Hour)},
		{"bytes=0-1, 4-5", ""}, {"bytes=0-4, 2-6", ""}, {"bytes=0-1, 50-60", ""}, {"bytes=0-10, 0-10", ""}, {"bytes=8-, -2, 3-3", ""},
		{"bytes=0-1, 4-5", tag}, {"bytes=0-1, 4-5", date(-time.Hour)}}

	client := &http.Client{}
	send := func(base, method string, fields []string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, base+"/a.txt", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept-Encoding", "identity")
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i+1] != "" {
				req.Header.Set(fields[i], fields[i+1])
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %v: %v", method, fields, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, resp.Header, body
	}
	compared, failed := 0, 0
	check := func(method string, fields []string) {
		t.Helper()
		gs, gh, gb := send(oracle.URL, method, fields)
		fs, fh, fb := send("http://"+addr, method, fields)
		compared++
		same := gs == fs
		if gs/100 == 2 || gs == 304 {
			for _, name := range []string{"ETag", "Last-Modified", "Content-Range"} {
				same = same && gh.Get(name) == fh.Get(name)
			}
		}
		if gs == 416 {
			same = same && gh.Get("Content-Range") == fh.Get("Content-Range")
		}
		if gs/100 == 2 {
			same = same && contentShape(t, gh, gb) == contentShape(t, fh, fb)
		}
		if !same && failed < 20 {
			failed++
			t.Errorf("%s %v: Go %d %q %q %q, std/http/static %d %q %q %q", method, fields,
				gs, gh.Get("ETag"), gh.Get("Content-Range"), contentShape(t, gh, gb), fs, fh.Get("ETag"), fh.Get("Content-Range"), contentShape(t, fh, fb))
		}
	}
	for _, im := range ifMatch {
		for _, ius := range ifUnmodified {
			for _, inm := range ifNoneMatch {
				for _, ims := range ifModified {
					pre := []string{"If-Match", im, "If-Unmodified-Since", ius, "If-None-Match", inm, "If-Modified-Since", ims}
					check("HEAD", pre)
					for _, r := range ranges {
						check("GET", append(append([]string{}, pre...), "Range", r[0], "If-Range", r[1]))
					}
				}
			}
		}
	}
	t.Logf("%d requests compared", compared)
}
