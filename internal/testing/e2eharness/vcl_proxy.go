package e2eharness

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

type VCLStart func(args ...string) *exec.Cmd

func CheckVCLProxyServesAndCaches(t *testing.T, startOrigin, startProxy VCLStart) {
	t.Helper()
	originPort := vclFreePort(t)
	proxyPort := vclFreePort(t)

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

	origin := startOrigin(fmt.Sprintf("%d", originPort))
	if err := origin.Start(); err != nil {
		t.Fatalf("start origin: %v", err)
	}
	defer func() { _ = origin.Process.Kill(); _ = origin.Wait() }()
	vclWaitForPort(t, originPort, "origin")

	proxy := startProxy(policyPath, fmt.Sprintf("%d", proxyPort))
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer func() { _ = proxy.Process.Kill(); _ = proxy.Wait() }()
	vclWaitForPort(t, proxyPort, "proxy")

	base := fmt.Sprintf("http://127.0.0.1:%d", proxyPort)

	first, hdr := vclGet(t, base+"/a")
	if !strings.Contains(first, "origin-hit=1") {
		t.Fatalf("first request should reach the origin, got %q", first)
	}
	if hdr.Get("X-Proxied-By") != "fern-vcl" {
		t.Errorf("vcl_backend_response header missing: %v", hdr)
	}
	if hdr.Get("X-Cache-Hits") != "0" {
		t.Errorf("a miss should report 0 hits, got %q", hdr.Get("X-Cache-Hits"))
	}

	second, hdr2 := vclGet(t, base+"/a")
	if second != first {
		t.Errorf("cached response differs.\nfirst:  %q\nsecond: %q", first, second)
	}
	if hdr2.Get("X-Cache-Hits") != "1" {
		t.Errorf("first hit should report 1, got %q", hdr2.Get("X-Cache-Hits"))
	}

	other, _ := vclGet(t, base+"/b")
	if !strings.Contains(other, "origin-hit=2") {
		t.Errorf("a different URL should reach the origin, got %q", other)
	}
	if !strings.Contains(other, "path=/b") {
		t.Errorf("the origin saw the wrong path: %q", other)
	}

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		body, _ := vclGet(t, base+"/nocache")
		if seen[body] {
			t.Fatalf("a passed request was served from cache: %q repeated", body)
		}
		seen[body] = true
	}

	again, _ := vclGet(t, base+"/a")
	if again != first {
		t.Errorf("object was lost from cache.\nwant: %q\ngot:  %q", first, again)
	}

	passed, _ := vclGetWithHeader(t, base+"/shared", "X-Bypass", "1")
	if !strings.Contains(passed, "path=/shared") {
		t.Fatalf("passed request did not reach the origin: %q", passed)
	}
	hashed, _ := vclGet(t, base+"/shared")
	if hashed == passed {
		t.Errorf("a passed request was inserted into the cache: %q served again", passed)
	}
}

func CheckVCLProxyRejectsABadPolicy(t *testing.T, startProxy VCLStart) {
	t.Helper()
	bad := filepath.Join(t.TempDir(), "bad.vcl")
	src := "vcl 4.1;\nbackend origin { .host = \"127.0.0.1\"; .port = \"9\"; }\n" +
		"sub vcl_backend_response { set beresp.http.X = req.url; return (deliver); }\n"
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	out, err := startProxy(bad, fmt.Sprintf("%d", vclFreePort(t))).CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "'req.url' is not readable in vcl_backend_response") {
		t.Errorf("expected the load-time scoping error, got:\n%s", out)
	}
}

func vclFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func vclWaitForPort(t *testing.T, port int, what string) {
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

func vclGet(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	return vclGetWithHeader(t, url, "", "")
}

func vclGetWithHeader(t *testing.T, url, name, value string) (string, http.Header) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if name != "" {
		req.Header.Set(name, value)
	}
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
