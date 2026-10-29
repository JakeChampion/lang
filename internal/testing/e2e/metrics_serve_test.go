package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
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

const metricsServeSource = `import "std/i32";
import "std/http";
import "std/platform";
import "std/serve";
import "std/metrics";
import "core/int";

function main(): i32 {
  let hits: metrics.Counter = metrics.counter("hits_total", "Requests the handler saw.");
  let port: i32 = 0;
  match (env("METRICS_PORT")) {
    Some(p) => {
      match (i32.parse_i32_radix(p, 10)) {
        Ok(n) => { port = n; },
        Err(_) => {}
      }
    },
    None => {}
  }
  let workers: i32 = 1;
  match (env("WORKERS")) {
    Some(w) => {
      match (i32.parse_i32_radix(w, 10)) {
        Ok(n) => { workers = n; },
        Err(_) => {}
      }
    },
    None => {}
  }
  let cfg: serve.Config = serve.Config { ...serve.config(), workers: workers, metrics: metrics.registry().with_counter(hits), metrics_port: port };
  return serve.exit_code(serve.supervise(0, cfg, (req: HttpRequest, plat: platform.Host): HttpResponse => {
    hits.inc();
    if (req.path == "/missing") {
      return http.not_found();
    }
    return http.ok("hi");
  }));
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
	ln := e2eharness.ListenInherited(t)
	port := freeLoopbackPort(t)
	cmd := exec.Command(bin, "-interp", src)
	cmd.Env = append(os.Environ(), fmt.Sprintf("METRICS_PORT=%d", port), "WORKERS=1")
	addr, _ := ln.Start(t, cmd)
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
	ln := e2eharness.ListenInherited(t)
	port := freeLoopbackPort(t)
	cmd := runX86Bin(qemu, out)
	cmd.Env = append(os.Environ(), fmt.Sprintf("METRICS_PORT=%d", port), "WORKERS=2")
	addr, stderrPath := ln.Start(t, cmd)
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

const metricsLargeScrapeSeries, metricsLargeScrapePad = 256, 32768

const metricsLargeScrapeSource = `import "std/async";
import "std/http";
import "std/i32";
import "std/metrics";

function port_from_env(): i32 {
  match (env("PORT")) {
    Some(p) => {
      match (i32.parse_i32_radix(p, 10)) {
        Ok(n) => { return n; },
        Err(_) => {}
      }
    },
    None => {}
  }
  return 0;
}

function main(): i32 {
  let pad: string = "x";
  let k: i32 = 0;
  while (k < 15) {
    pad = pad + pad;
    k = k + 1;
  }
  let reg: metrics.Registry = metrics.registry();
  let i: i32 = 0;
  while (i < 256) {
    let c: metrics.Counter = metrics.counter("scrape_padding_total", "Padding.").with_labels(metrics.labels([metrics.label("n", i.to_string()), "pad=\"" + pad + "\""]));
    c.inc();
    reg = reg.with_counter(c);
    i = i + 1;
  }
  let text: string = reg.render();
  let drv: async.RealDriver = async.real_driver();
  let Ok(listening) = metrics.listen(drv, port_from_env()) else {
    return 1;
  };
  let ex: metrics.Exporter = listening;
  let quit: Cell[boolean] = cell_new(false);
  while (!quit.get()) {
    let events: i32[] = drv.wait(16, None);
    let at: i32 = 0;
    while (at + 1 < events.len()) {
      let fd: i32 = events[at];
      at = at + 2;
      if (ex.owns(fd)) {
        ex = ex.wake(drv, fd, (path: string): HttpResponse => {
          if (path == "/quit") {
            quit.set(true);
          }
          return metrics.scrape(path, (): string => text, true);
        });
      }
    }
  }
  ex.close(drv);
  return 0;
}
`

// TestMetricsExporterLargeScrape scrapes a response several times larger than a socket's send buffer, so the
// exporter must keep writing as the socket drains rather than close after the first partial write.
func TestMetricsExporterLargeScrape(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "large_scrape.fern")
	if err := os.WriteFile(src, []byte(metricsLargeScrapeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, target, out string) {
		t.Helper()
		if o, err := exec.Command(buildFernCLI(t), "-target", target, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build for %s failed: %v\n%s", target, err, o)
		}
	}
	t.Run("interp", func(t *testing.T) {
		checkLargeScrape(t, exec.Command(buildLangBinForInterp(t), "-interp", src))
	})
	t.Run("x86_64", func(t *testing.T) {
		qemu := x86QemuOrEmpty(t)
		out := filepath.Join(dir, "large_scrape_x86.bin")
		build(t, "x86-64-linux", out)
		checkLargeScrape(t, runX86Bin(qemu, out))
	})
	t.Run("arm64", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(dir, "large_scrape_arm64.bin")
		build(t, "arm64-linux", out)
		checkLargeScrape(t, runArm64Bin(qemu, out))
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		out := filepath.Join(dir, "large_scrape.wasm")
		build(t, "wasm32-wasi", out)
		checkLargeScrape(t, exec.Command("wasmtime", "run", "-S", "inherit-network", "--env", "PORT", out))
	})
}

func checkLargeScrape(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	port := freeLoopbackPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-exited
	})
	e2eharness.WaitServerReady(t, addr)

	pad := strings.Repeat("x", metricsLargeScrapePad)
	var want strings.Builder
	want.WriteString("# HELP scrape_padding_total Padding.\n# TYPE scrape_padding_total counter\n")
	for i := 0; i < metricsLargeScrapeSeries; i++ {
		fmt.Fprintf(&want, "scrape_padding_total{n=\"%d\",pad=\"%s\"} 1\n", i, pad)
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := conn.Write([]byte("GET /metrics HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	// Reading late lets the exporter's first write fill the socket and come back partial.
	time.Sleep(300 * time.Millisecond)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v\nstderr:\n%s", err, stderr.String())
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("body after %d of %d bytes: %v\nstderr:\n%s", len(body), resp.ContentLength, err, stderr.String())
	}
	if resp.StatusCode != 200 || string(body) != want.String() {
		t.Fatalf("scrape: status %d, %d body bytes, want 200 and the %d-byte exposition\nstderr:\n%s", resp.StatusCode, len(body), want.Len(), stderr.String())
	}

	if quit := e2eharness.HTTPRoundTrip(t, addr, "/quit", 10*time.Second); !strings.Contains(quit, "HTTP/1.1 404") {
		t.Fatalf("GET /quit: want 404, got\n%s", quit)
	}
	select {
	case err := <-exited:
		exited <- err
		if err != nil {
			t.Fatalf("exporter exited with %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("exporter still running 30s after /quit\nstderr:\n%s", stderr.String())
	}
}
