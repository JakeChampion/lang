package e2e

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
import "std/http/cors";
import "std/http/router";
import "std/platform";
import "std/serve";

function cross_origin(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  return cors.cors(req, cors.policy(["https://app.example.com"]).with_methods(["GET", "POST"]), next);
}

function show(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  let id: i32 = params.int("id")?;
  return Ok(http.json("{\"id\":" + id.to_string() + "}"));
}

function create(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  let b: string = req.body_string()?;
  return Ok(http.created("made " + b));
}

function files(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  match (params.get("path")) {
    Some(p) => {
      return Ok(http.ok("file " + p));
    },
    None => {
      return Ok(http.ok("files"));
    }
  }
  return Ok(http.ok(""));
}

function main(): i32 {
  return serve.exit_code(serve.run(0, serve.config(), router.new[platform.Host]().wrap(cross_origin).get("/users/{id}", show).post("/users", create).get("/files/{path...}", files).handler()));
}
`

func TestRouterRefusedPatternExitsInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeRouterRefusedSource(t)
	checkRouterRefusedExit(t, exec.Command(bin, "-interp", src))
}

func TestRouterRefusedPatternExitsX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	src := writeRouterRefusedSource(t)
	out := filepath.Join(filepath.Dir(src), "router_refused.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	checkRouterRefusedExit(t, runX86Bin(qemu, out))
}

func writeRouterRefusedSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "router_refused.fern")
	if err := os.WriteFile(src, []byte(routerRefusedSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

const routerRefusedSource = `import "std/http";
import "std/http/router";
import "std/platform";
import "std/serve";

function hello(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
  return Ok(http.ok("hello"));
}

function main(): i32 {
  let r: router.Router[platform.Host] = router.new[platform.Host]().get("/hello", hello).get("/a/{x...}/b", hello).get("/late", hello);
  print("built");
  let h: (HttpRequest, platform.Host) => HttpResponse = r.handler();
  print("served");
  return serve.exit_code(serve.run(0, serve.config(), h));
}
`

func checkRouterRefusedExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 2 {
		t.Fatalf("exit = %d (%v), want 2\nstdout: %s\nstderr: %s", code, err, stdout.String(), stderr.String())
	}
	if got := stdout.String(); got != "built\n" {
		t.Errorf("stdout = %q, want only the line before handler()", got)
	}
	want := "http router: pattern GET /a/{x...}/b: {name...} must be the last segment"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to name the pattern: %q", stderr.String(), want)
	}
}

func checkRouterServe(t *testing.T, addr string) {
	t.Helper()
	cases := []struct {
		method, path, body string
		status             int
		allow, want        string
		origin, acao       string
	}{
		{"GET", "/users/42", "", 200, "", `{"id":42}`, "", ""},
		{"GET", "/users/42", "", 200, "", `{"id":42}`, "https://app.example.com", "https://app.example.com"},
		{"OPTIONS", "/users", "", 204, "", "", "https://app.example.com", "https://app.example.com"},
		{"OPTIONS", "/users", "", 405, "POST", "", "", ""},
		{"GET", "/users/x1", "", 400, "", `"detail":"path parameter id is not an integer: x1"`, "", ""},
		{"HEAD", "/users/42", "", 200, "", "", "", ""},
		{"PUT", "/users/42", "", 405, "GET, HEAD", "", "", ""},
		{"PUT", "/users", "", 405, "POST", "", "", ""},
		{"POST", "/users", "ann", 201, "", "made ann", "", ""},
		{"GET", "/files/a/b%20c.txt", "", 200, "", "file a/b c.txt", "", ""},
		{"GET", "/files/a%2541", "", 200, "", "file a%41", "", ""},
		{"GET", "/files/", "", 200, "", "file ", "", ""},
		{"GET", "/files", "", 404, "", "", "", ""},
		{"GET", "/nope?x=1", "", 404, "", "", "", ""},
	}
	client := &http.Client{}
	for _, c := range cases {
		req, err := http.NewRequest(c.method, "http://"+addr+c.path, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
			if c.method == "OPTIONS" {
				req.Header.Set("Access-Control-Request-Method", "POST")
			}
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
		if a := resp.Header.Get("Access-Control-Allow-Origin"); a != c.acao {
			t.Errorf("%s %s (Origin %q): Access-Control-Allow-Origin %q, want %q", c.method, c.path, c.origin, a, c.acao)
		}
		if c.want != "" && !strings.Contains(string(got), c.want) {
			t.Errorf("%s %s: body %q, want it to contain %q", c.method, c.path, got, c.want)
		}
		if c.method == "HEAD" && len(got) != 0 {
			t.Errorf("HEAD %s: body %q, want none", c.path, got)
		}
	}
}
