package e2e

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/http/router under std/serve's loop: a route with a typed parameter,
// one taking the rest of the path, 405 with Allow, 404, and HEAD answered
// by a GET route. The interpreter and the x86-64 backend.
func TestRouterServeInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeRouterServeSource(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", src))
	checkRouterServe(t, addr)
}

func TestRouterServeX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeRouterServeSource(t)
	out := filepath.Join(filepath.Dir(src), "router_serve.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, out))
	checkRouterServe(t, addr)
}

func writeRouterServeSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "router_serve.fern")
	if err := os.WriteFile(src, []byte(routerServeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const routerServeSource = `import "std/http";
import "std/http/router";
import "std/platform";
import "std/serve";

function show(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
  return http.respond(show_result(params));
}

function show_result(params: router.Params): Result[HttpResponse, http.HttpError] {
  let id: i32 = params.int("id")?;
  return Ok(http.json("{\"id\":" + id.to_string() + "}"));
}

function create(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
  match (req.body_string()) {
    Ok(b) => {
      return http.created("made " + b);
    },
    Err(e) => {
      return http.bad_request("body");
    }
  }
  return http.bad_request("body");
}

function files(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
  match (params.get("path")) {
    Some(p) => {
      return http.ok("file " + p);
    },
    None => {
      return http.ok("files");
    }
  }
  return http.ok("");
}

function routes(): Result[router.Router[platform.Host], router.RouteError] {
  let r: router.Router[platform.Host] = router.new();
  r = r.get("/users/{id}", show)?;
  r = r.post("/users", create)?;
  r = r.get("/files/{path...}", files)?;
  return Ok(r);
}

function main(): i32 {
  match (routes()) {
    Ok(r) => {
      return serve.run(0, serve.config(), r.handler());
    },
    Err(e) => {
      print(e.message());
      return 2;
    }
  }
  return 2;
}
`

func checkRouterServe(t *testing.T, addr string) {
	t.Helper()
	cases := []struct {
		method, path, body string
		status             int
		allow, want        string
	}{
		{"GET", "/users/42", "", 200, "", `{"id":42}`},
		{"GET", "/users/x1", "", 400, "", `"detail":"path parameter id is not an integer: x1"`},
		{"HEAD", "/users/42", "", 200, "", ""},
		{"PUT", "/users/42", "", 405, "GET, HEAD", ""},
		{"PUT", "/users", "", 405, "POST", ""},
		{"POST", "/users", "ann", 201, "", "made ann"},
		{"GET", "/files/a/b%20c.txt", "", 200, "", "file a/b c.txt"},
		{"GET", "/files/a%2541", "", 200, "", "file a%41"},
		{"GET", "/files/", "", 200, "", "file "},
		{"GET", "/files", "", 404, "", ""},
		{"GET", "/nope?x=1", "", 404, "", ""},
	}
	client := &http.Client{}
	for _, c := range cases {
		req, err := http.NewRequest(c.method, "http://"+addr+c.path, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("%s %s: status %d, want %d\n%s", c.method, c.path, resp.StatusCode, c.status, got)
		}
		if a := resp.Header.Get("Allow"); a != c.allow {
			t.Errorf("%s %s: Allow %q, want %q", c.method, c.path, a, c.allow)
		}
		if c.want != "" && !strings.Contains(string(got), c.want) {
			t.Errorf("%s %s: body %q, want it to contain %q", c.method, c.path, got, c.want)
		}
		if c.method == "HEAD" && len(got) != 0 {
			t.Errorf("HEAD %s: body %q, want none", c.path, got)
		}
	}
}
