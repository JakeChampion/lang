package e2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// examples/vcl/ ends in a real HTTP caching reverse proxy: a listening
// socket, a request parsed off the wire, VCL deciding what happens, a real
// TCP fetch from the declared backend on a miss, an in-memory cache that
// survives between requests, and a real response written back.
//
// This gate runs it. It builds the proxy and a counting origin, puts one
// in front of the other, and drives them with a real HTTP client.
//
// The origin is what makes caching PROVABLE. Every response carries the
// number of requests that process has actually served, so a second request
// answered with the SAME counter means the origin was never asked. Reading
// the proxy's own headers could never establish that — only the origin can.
//
// The sockets are native builtins, absent from the interpreter, so
// everything here is compiled for the host.

// freePort asks the OS for a port and immediately releases it. A fixed
// port would be flaky for a worse reason than the race this has: the
// listener does not set SO_REUSEADDR, so a port used by an earlier run is
// still in TIME_WAIT and binding it fails outright.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitForPort blocks until something accepts on the port, so the test does
// not race the server's startup.
func waitForPort(t *testing.T, port int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never came up on port %d", what, port)
}

// buildVCLBinary compiles one of the example's programs for the host.
func buildVCLBinary(t *testing.T, bin, src, out string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), out)
	cmd := exec.Command(bin, "-target", hostFernTarget(t), "-o", exe, src)
	cmd.Dir = langSrcAbs(t, "examples/vcl")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", src, err, o)
	}
	return exe
}

// get fetches a URL and returns the body plus the response headers.
func get(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	return string(body), resp.Header
}

// TestVCLProxyServesAndCaches is the end of the line for this example: a
// policy, a real origin, and real HTTP through a real socket.
func TestVCLProxyServesAndCaches(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs two servers; skipped under -short")
	}
	bin := buildLangBinForInterp(t)

	originPort := freePort(t)
	proxyPort := freePort(t)

	originExe := buildVCLBinary(t, bin, "origin.fern", "origin")
	proxyExe := buildVCLBinary(t, bin, "vclproxy.fern", "vclproxy")

	// The policy names the origin's port, which is chosen at run time.
	policy := fmt.Sprintf(`vcl 4.1;

backend origin {
    .host = "127.0.0.1";
    .port = "%d";
}

sub vcl_recv {
    if (req.http.X-Bypass) {
        return (pass);
    }
    if (req.url ~ "^/nocache") {
        return (pass);
    }
    return (hash);
}

sub vcl_hash {
    hash_data(req.url);
}

sub vcl_backend_response {
    set beresp.http.X-Proxied-By = "fern-vcl";
    return (deliver);
}

sub vcl_deliver {
    set resp.http.X-Cache-Hits = obj.hits;
    return (deliver);
}
`, originPort)
	policyPath := filepath.Join(t.TempDir(), "proxy.vcl")
	if err := os.WriteFile(policyPath, []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	origin := exec.Command(originExe, fmt.Sprintf("%d", originPort))
	if err := origin.Start(); err != nil {
		t.Fatalf("start origin: %v", err)
	}
	defer func() { _ = origin.Process.Kill(); _ = origin.Wait() }()
	waitForPort(t, originPort, "origin")

	proxy := exec.Command(proxyExe, policyPath, fmt.Sprintf("%d", proxyPort))
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer func() { _ = proxy.Process.Kill(); _ = proxy.Wait() }()
	waitForPort(t, proxyPort, "proxy")

	base := fmt.Sprintf("http://127.0.0.1:%d", proxyPort)

	// A miss reaches the origin, and the policy's header is on the way back.
	first, hdr := get(t, base+"/a")
	if !strings.Contains(first, "origin-hit=1") {
		t.Fatalf("first request should reach the origin, got %q", first)
	}
	if hdr.Get("X-Proxied-By") != "fern-vcl" {
		t.Errorf("vcl_backend_response header missing: %v", hdr)
	}
	if hdr.Get("X-Cache-Hits") != "0" {
		t.Errorf("a miss should report 0 hits, got %q", hdr.Get("X-Cache-Hits"))
	}

	// The same URL again must NOT reach the origin: an unchanged counter is
	// the proof, and obj.hits climbs.
	second, hdr2 := get(t, base+"/a")
	if second != first {
		t.Errorf("cached response differs.\nfirst:  %q\nsecond: %q", first, second)
	}
	if hdr2.Get("X-Cache-Hits") != "1" {
		t.Errorf("first hit should report 1, got %q", hdr2.Get("X-Cache-Hits"))
	}

	// A different URL hashes differently, so it is a separate object.
	other, _ := get(t, base+"/b")
	if !strings.Contains(other, "origin-hit=2") {
		t.Errorf("a different URL should reach the origin, got %q", other)
	}
	if !strings.Contains(other, "path=/b") {
		t.Errorf("the origin saw the wrong path: %q", other)
	}

	// `return (pass)` must never cache: every request reaches the origin.
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		body, _ := get(t, base+"/nocache")
		if seen[body] {
			t.Fatalf("a passed request was served from cache: %q repeated", body)
		}
		seen[body] = true
	}

	// And the first object is still cached after all that traffic.
	again, _ := get(t, base+"/a")
	if again != first {
		t.Errorf("object was lost from cache.\nwant: %q\ngot:  %q", first, again)
	}

	// A pass must not INSERT either, which a URL that is always passed
	// cannot show: a pass never looks up, so storing on that path is
	// invisible from it. The same URL passed once and then hashed is what
	// exposes it — if the pass inserted, this second request would be
	// served the stored object instead of reaching the origin.
	passed, _ := getWithHeader(t, base+"/shared", "X-Bypass", "1")
	if !strings.Contains(passed, "path=/shared") {
		t.Fatalf("passed request did not reach the origin: %q", passed)
	}
	hashed, _ := get(t, base+"/shared")
	if hashed == passed {
		t.Errorf("a passed request was inserted into the cache: %q served again", passed)
	}
}

// getWithHeader fetches a URL with one extra request header.
func getWithHeader(t *testing.T, url, name, value string) (string, http.Header) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(name, value)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	return string(body), resp.Header
}

// TestVCLProxyRejectsABadPolicyAtLoadTime pins that the proxy checks its
// policy before it ever binds a port — a scoping error must not wait for
// traffic to find it.
func TestVCLProxyRejectsABadPolicyAtLoadTime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary; skipped under -short")
	}
	bin := buildLangBinForInterp(t)
	proxyExe := buildVCLBinary(t, bin, "vclproxy.fern", "vclproxy")

	bad := filepath.Join(t.TempDir(), "bad.vcl")
	src := "vcl 4.1;\nbackend origin { .host = \"127.0.0.1\"; .port = \"9\"; }\n" +
		"sub vcl_backend_response { set beresp.http.X = req.url; return (deliver); }\n"
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	out, err := exec.Command(proxyExe, bad, fmt.Sprintf("%d", freePort(t))).CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "'req.url' is not readable in vcl_backend_response") {
		t.Errorf("expected the load-time scoping error, got:\n%s", out)
	}
}
