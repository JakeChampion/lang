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

func TestForwardedServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeForwardedServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkForwardedServe(t, addr)
}

func TestForwardedServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeForwardedServeSource(t)
	out := filepath.Join(filepath.Dir(src), "forwarded_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkForwardedServe(t, addr)
}

func writeForwardedServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "forwarded_serve.fern")
	if err := os.WriteFile(src, []byte(forwardedServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const forwardedServeSource = `import "std/http";
import "std/http/forwarded";
import "std/platform";
import "std/serve";

function text(s: Option[string]): string {
  match (s) {
    Some(x) => {
      return x;
    },
    None => {
      return "-";
    }
  }
}

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  let peer: string = "-";
  match (plat.peer()) {
    Some(p) => {
      peer = p.ip.to_string();
    },
    None => {}
  }
  match (forwarded.trust(["127.0.0.0/8", "::1"])) {
    Ok(t) => {
      let c: forwarded.Client = forwarded.client(req, plat, t);
      let ip: string = "-";
      match (c.ip()) {
        Some(a) => {
          ip = a.to_string();
        },
        None => {}
      }
      return http.ok(peer + " " + ip + " " + text(c.proto) + " " + text(c.host));
    },
    Err(bad) => {
      return http.internal_error(bad);
    }
  }
  return http.internal_error("");
}

function main(): i32 {
  return serve.run(0, serve.config(), handle);
}
`

func checkForwardedServe(t *testing.T, addr string) {
	t.Helper()
	cases := []struct {
		name, forwarded, want string
	}{
		{"no field", "", "127.0.0.1 127.0.0.1 - " + addr},
		{"one hop", "for=192.0.2.60;proto=https;host=shop.example", "127.0.0.1 192.0.2.60 https shop.example"},
		{"forged hop", "for=10.9.9.9, for=192.0.2.61;proto=http", "127.0.0.1 192.0.2.61 http " + addr},
		{"malformed", "for=192.0.2.60;for=192.0.2.61", "127.0.0.1 127.0.0.1 - " + addr},
	}
	for _, c := range cases {
		req, err := http.NewRequest("GET", "http://"+addr+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.forwarded != "" {
			req.Header.Set("Forwarded", c.forwarded)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(got) != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
