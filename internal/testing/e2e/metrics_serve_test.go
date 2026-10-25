package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const metricsServeSource = `import "std/http";
import "std/platform";
import "std/serve";
import "std/metrics";
import "core/int";

function main(): i32 {
  let hits: metrics.Counter = metrics.counter("hits_total", "Requests the handler saw.");
  let port: i32 = 0;
  match (env("METRICS_PORT")) {
    Some(p) => {
      match (int.parse_int_radix(p, 10)) {
        Some(n) => { port = n; },
        None => {}
      }
    },
    None => {}
  }
  let workers: i32 = 1;
  match (env("WORKERS")) {
    Some(w) => {
      match (int.parse_int_radix(w, 10)) {
        Some(n) => { workers = n; },
        None => {}
      }
    },
    None => {}
  }
  let cfg: serve.Config = serve.Config { ...serve.config(), workers: workers, metrics: metrics.registry().with_counter(hits), metrics_port: port };
  return serve.supervise(0, cfg, (req: HttpRequest, plat: platform.Host): HttpResponse => {
    hits.inc();
    if (req.path == "/missing") {
      return http.not_found();
    }
    return http.ok("hi");
  });
}
`

func writeMetricsServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "metrics_serve.fern")
	if err := os.WriteFile(src, []byte(metricsServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestMetricsServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeMetricsServeSource(t)
	port := freeLoopbackPort(t)
	cmd := exec.Command(bin, "-interp", src)
	cmd.Env = append(os.Environ(), fmt.Sprintf("METRICS_PORT=%d", port), "WORKERS=1")
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	checkMetricsServe(t, addr, fmt.Sprintf("127.0.0.1:%d", port), 1)
}

func TestMetricsSupervisedServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeMetricsServeSource(t)
	out := filepath.Join(filepath.Dir(src), "metrics_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	port := freeLoopbackPort(t)
	cmd := runX86Bin(qemu, out)
	cmd.Env = append(os.Environ(), fmt.Sprintf("METRICS_PORT=%d", port), "WORKERS=2")
	addr, stderrPath := e2eharness.StartInheritedServer(t, cmd)
	checkMetricsServe(t, addr, fmt.Sprintf("127.0.0.1:%d", port), 2)
	if b, _ := os.ReadFile(stderrPath); strings.Contains(string(b), "worker died") {
		t.Fatalf("a worker died:\n%s", b)
	}
}

func checkMetricsServe(t *testing.T, addr, metricsAddr string, workers int) {
	t.Helper()
	e2eharness.WaitServerReady(t, addr)
	e2eharness.WaitServerReady(t, metricsAddr)
	const gets, posts, misses = 5, 2, 3
	for i := 0; i < gets; i++ {
		if resp := e2eharness.HTTPRoundTrip(t, addr, "/hello", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
			t.Fatalf("GET /hello: want 200, got\n%s", resp)
		}
	}
	for i := 0; i < misses; i++ {
		if resp := e2eharness.HTTPRoundTrip(t, addr, "/missing", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 404") {
			t.Fatalf("GET /missing: want 404, got\n%s", resp)
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < posts; i++ {
		resp, err := client.Post("http://"+addr+"/hello", "text/plain", strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("POST /hello: want 200, got %d", resp.StatusCode)
		}
	}
	total := gets + posts + misses
	var text string
	deadline := time.Now().Add(10 * time.Second)
	for {
		text = scrape(t, metricsAddr, "/metrics", 200, "text/plain; version=0.0.4; charset=utf-8")
		if sampleValue(text, "hits_total") == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hits_total never reached %d:\n%s", total, text)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, want := range []struct {
		key   string
		value int
	}{
		{`http_server_request_duration_seconds_count{http_request_method="GET",http_response_status_code="200"}`, gets},
		{`http_server_request_duration_seconds_count{http_request_method="GET",http_response_status_code="404"}`, misses},
		{`http_server_request_duration_seconds_count{http_request_method="POST",http_response_status_code="200"}`, posts},
		{`http_server_request_duration_seconds_bucket{http_request_method="GET",http_response_status_code="200",le="+Inf"}`, gets},
	} {
		if got := sampleValue(text, want.key); got != want.value {
			t.Errorf("%s = %d, want %d\n%s", want.key, got, want.value, text)
		}
	}
	if got := sampleValue(text, "http_server_connections_accepted_total"); got < total {
		t.Errorf("http_server_connections_accepted_total = %d, want at least %d (one per request, each on its own connection)", got, total)
	}
	for _, head := range []string{
		"# TYPE hits_total counter",
		"# HELP hits_total Requests the handler saw.",
		"# TYPE http_server_request_duration_seconds histogram",
		"# TYPE http_server_open_connections gauge",
	} {
		if strings.Count(text, head) != 1 {
			t.Errorf("%q appears %d times, want once\n%s", head, strings.Count(text, head), text)
		}
	}
	if body := scrape(t, metricsAddr, "/healthz", 200, ""); body != "ok\n" {
		t.Errorf("/healthz = %q", body)
	}
	if body := scrape(t, metricsAddr, "/readyz", 200, ""); body != "ready\n" {
		t.Errorf("/readyz = %q", body)
	}
	scrape(t, metricsAddr, "/metrics?format=text", 200, "text/plain; version=0.0.4; charset=utf-8")
	scrape(t, metricsAddr, "/nope", 404, "")
	if resp := e2eharness.HTTPRoundTrip(t, metricsAddr, "/metrics", 5*time.Second); !strings.Contains(resp, "Connection: close") {
		t.Errorf("a scrape's response does not close the connection:\n%s", resp)
	}
}

func scrape(t *testing.T, metricsAddr, path string, status int, contentType string) string {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + metricsAddr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("GET %s: status %d, want %d\n%s", path, resp.StatusCode, status, body)
	}
	if contentType != "" && resp.Header.Get("Content-Type") != contentType {
		t.Errorf("GET %s: Content-Type %q, want %q", path, resp.Header.Get("Content-Type"), contentType)
	}
	return string(body)
}

var sampleLine = regexp.MustCompile(`(?m)^(\S+) (-?\d+)$`)

func sampleValue(text, key string) int {
	for _, m := range sampleLine.FindAllStringSubmatch(text, -1) {
		if m[1] == key {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				return -1
			}
			return n
		}
	}
	return -1
}
