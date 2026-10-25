package e2e

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestHTTPAllowServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeHTTPAllowServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkHTTPAllowServe(t, addr)
}

func TestHTTPAllowServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeHTTPAllowServeSource(t)
	out := filepath.Join(filepath.Dir(src), "http_allow_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkHTTPAllowServe(t, addr)
}

func writeHTTPAllowServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "http_allow_serve.fern")
	if err := os.WriteFile(src, []byte(httpAllowServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const httpAllowServeSource = `import "std/http";
import "std/fetch";
import "std/platform";
import "std/serve";

function outcome(got: Result[HttpResponse, fetch.FetchError]): string {
  match (got) {
    Ok(resp) => {
      return "status " + resp.status.to_string();
    },
    Err(e) => {
      return e.message();
    }
  }
  return "";
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  let off: string = outcome(plat.http(fetch.get("http://denied.example/")));
  let listed: string = outcome(plat.http(fetch.get("http://127.0.0.1/")));
  return http.ok(off + "|" + listed);
}

function main(): i32 {
  match (fetch.allow(["127.0.0.1"])) {
    Ok(a) => {
      return serve.exit_code(serve.run(0, serve.Config { ...serve.config(), http_allow: Some(a) }, handle));
    },
    Err(why) => {
      print(why);
      return 1;
    }
  }
  return 1;
}
`

func checkHTTPAllowServe(t *testing.T, addr string) {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := "blocked: denied.example:80 is not on the allow-list|blocked: 127.0.0.1 is not a global address"
	if string(got) != want {
		t.Errorf("%q, want %q", got, want)
	}
}
