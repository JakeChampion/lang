package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestAccessLogServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeAccessLogServeSource(t)
	addr, stderrPath := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkAccessLogServe(t, addr, stderrPath)
}

func TestAccessLogServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeAccessLogServeSource(t)
	out := filepath.Join(filepath.Dir(src), "accesslog_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, stderrPath := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkAccessLogServe(t, addr, stderrPath)
}

func writeAccessLogServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "accesslog_serve.fern")
	if err := os.WriteFile(src, []byte(accessLogServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const accessLogServeSource = `import "std/http";
import "std/http/router";
import "std/http/accesslog";
import "std/platform";
import "std/serve";

function users_show(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  use req <- accesslog.logged(req, plat);
  let id: string = params.param("id")?;
  return Ok(http.ok("user " + id));
}

function main(): i32 {
  return serve.exit_code(serve.run(0, serve.Config { ...serve.config(), trace: true }, router.new[platform.Host]().get("/users/{id}", users_show).handler()));
}
`

type accessRecord struct {
	TimeUnixNano   string         `json:"timeUnixNano"`
	SeverityNumber int            `json:"severityNumber"`
	SeverityText   string         `json:"severityText"`
	Body           string         `json:"body"`
	TraceID        string         `json:"traceId"`
	SpanID         string         `json:"spanId"`
	Attributes     map[string]any `json:"attributes"`
}

func checkAccessLogServe(t *testing.T, addr, stderrPath string) {
	t.Helper()
	req, err := http.NewRequest("GET", "http://"+addr+"/users/7", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("User-Agent", "accesslog-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "user 7" {
		t.Fatalf("body %q, want %q", body, "user 7")
	}
	logged := e2eharness.WaitStderrContains(t, stderrPath, `"body":"GET /users/7 200"`, 10*time.Second)
	var rec accessRecord
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, `"body":"GET /users/7 200"`) {
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("the record is not JSON: %v\n%s", err, line)
			}
		}
	}
	if rec.SeverityNumber != 9 || rec.SeverityText != "INFO" || rec.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || len(rec.SpanID) != 16 || rec.SpanID == "00f067aa0ba902b7" {
		t.Errorf("record %+v", rec)
	}
	attrs := rec.Attributes
	if attrs["http.request.method"] != "GET" || attrs["url.path"] != "/users/7" || attrs["http.response.status_code"] != float64(200) || attrs["user_agent.original"] != "accesslog-test" || attrs["http.response.body.size"] != float64(6) {
		t.Errorf("attributes %v", attrs)
	}
	if _, ok := attrs["http.server.request.duration"].(float64); !ok {
		t.Errorf("duration %v, want seconds as a number", attrs["http.server.request.duration"])
	}
	if ip, _ := attrs["client.address"].(string); ip != "127.0.0.1" && ip != "::1" && ip != "::ffff:127.0.0.1" {
		t.Errorf("client.address %v, want the loopback peer", attrs["client.address"])
	}
}
