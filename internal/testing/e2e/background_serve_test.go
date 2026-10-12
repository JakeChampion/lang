package e2e

import (
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

// A response's background work (`with_background`) through std/serve's
// loop: the response reaches the client before the work, which sleeps a
// second, has logged; and work past `Config.background_deadline` is cut
// off on x86-64, where the loop cancels it, while the interpreter, which
// runs a task to completion, lets it finish.
func TestBackgroundServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeBackgroundServeSource(t)
	addr, stderrPath := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkBackgroundServe(t, addr, stderrPath, "forever: ran to the end")
}

func TestBackgroundServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeBackgroundServeSource(t)
	out := filepath.Join(filepath.Dir(src), "background_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, stderrPath := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkBackgroundServe(t, addr, stderrPath, "forever: cut off")
}

func writeBackgroundServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "background_serve.fern")
	if err := os.WriteFile(src, []byte(backgroundServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const backgroundServeSource = `import "std/http";
import "std/platform";
import "std/serve";
import "std/time";

function forever(plat: platform.Host): void {
  plat.sleep(time.duration_millis(1500 as i64));
  if (plat.cancelled()) {
    plat.log("forever: cut off");
  } else {
    plat.log("forever: ran to the end");
  }
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/forever") {
    return http.ok("started").with_background(() => forever(plat));
  }
  return http.ok("sent").with_background(() => {
    plat.sleep(time.duration_seconds(1 as i64));
    plat.log("background: first");
  }).with_background(() => plat.log("background: second"));
}

function main(): i32 {
  return serve.run(0, serve.Config { ...serve.config(), background_deadline: time.duration_millis(300 as i64) }, handle);
}
`

// checkBackgroundServe asks `/`, whose two pieces of work log in order once
// the response is out, then `/forever`, whose work logs `forever`.
func checkBackgroundServe(t *testing.T, addr, stderrPath, forever string) {
	t.Helper()
	get := func(path string) string {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}
	if got := get("/"); got != "sent" {
		t.Fatalf("/: %q, want %q", got, "sent")
	}
	if logged, _ := os.ReadFile(stderrPath); strings.Contains(string(logged), "background:") {
		t.Errorf("the work logged before its response arrived:\n%s", logged)
	}
	logged := e2eharness.WaitStderrContains(t, stderrPath, "background: second", 10*time.Second)
	if first, second := strings.Index(logged, "background: first"), strings.Index(logged, "background: second"); first < 0 || second < first {
		t.Errorf("the pieces of work ran out of order:\n%s", logged)
	}
	if got := get("/forever"); got != "started" {
		t.Fatalf("/forever: %q, want %q", got, "started")
	}
	e2eharness.WaitStderrContains(t, stderrPath, forever, 10*time.Second)
}
