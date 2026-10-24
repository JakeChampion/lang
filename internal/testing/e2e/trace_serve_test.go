package e2e

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestTraceServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeTraceServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkTraceServe(t, addr)
}

func TestTraceServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeTraceServeSource(t)
	out := filepath.Join(filepath.Dir(src), "trace_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkTraceServe(t, addr)
}

func writeTraceServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "trace_serve.fern")
	if err := os.WriteFile(src, []byte(traceServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const traceServeSource = `import "std/http";
import "std/platform";
import "std/serve";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  match (plat.trace()) {
    Some(span) => {
      return http.ok(span.header() + " " + span.state);
    },
    None => {
      return http.ok("untraced");
    }
  }
  return http.ok("");
}

function main(): i32 {
  return serve.run(0, serve.Config { ...serve.config(), trace: true }, handle);
}
`

var traceparentShape = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

func checkTraceServe(t *testing.T, addr string) {
	t.Helper()
	get := func(fields map[string]string) string {
		req, err := http.NewRequest("GET", "http://"+addr+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range fields {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}
	got := get(map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"tracestate":  "rojo=00f067aa0ba902b7",
	})
	parent, state, _ := strings.Cut(got, " ")
	m := traceparentShape.FindStringSubmatch(parent)
	if m == nil || m[1] != "4bf92f3577b34da6a3ce929d0e0e4736" || m[2] == "00f067aa0ba902b7" || m[3] != "01" || state != "rojo=00f067aa0ba902b7" {
		t.Errorf("continued: %q, want the caller's trace under a new span, its state kept", got)
	}
	fresh, _, _ := strings.Cut(get(map[string]string{"traceparent": "garbage"}), " ")
	if m := traceparentShape.FindStringSubmatch(fresh); m == nil || m[1] == "4bf92f3577b34da6a3ce929d0e0e4736" || m[3] != "03" {
		t.Errorf("started: %q, want a new sampled trace with a random id", fresh)
	}
}
