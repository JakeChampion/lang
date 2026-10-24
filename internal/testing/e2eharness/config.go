package e2eharness

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"
)

func ConfigHandlerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/greeting") {
        match (plat.config("GREETING")) { Some(v) => { return http.ok(v); }, None => { return http.ok("unset"); } }
    }
    if (req.path == "/token") {
        match (plat.secret("TOKEN")) { Some(v) => { return http.ok(v.len().to_string()); }, None => { return http.ok("unset"); } }
    }
    match (plat.config("FERN_CONFIG_NOT_SET_ANYWHERE")) { Some(v) => { return http.ok("set: " + v); }, None => { return http.ok("unset"); } }
}
`
}

var ConfigHandlerEnv = []string{"GREETING=hello", "TOKEN=s3cr3t"}

func CheckConfigHandler(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, c := range []struct{ path, want string }{
		{"/greeting", "hello"},
		{"/token", "6"},
		{"/other", "unset"},
	} {
		resp, err := client.Get("http://" + addr + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || string(body) != c.want {
			t.Errorf("%s: %d %q (%v), want 200 %q", c.path, resp.StatusCode, body, err, c.want)
		}
	}
}

func ServeComponentWithConfig(t *testing.T, wasmtime, component string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	args := []string{"serve", "--addr", addr, "-S", "config"}
	for _, kv := range ConfigHandlerEnv {
		args = append(args, "-S", "config-var="+kv)
	}
	srv := exec.Command(wasmtime, append(args, component)...)
	var log bytes.Buffer
	srv.Stdout, srv.Stderr = &log, &log
	if err := srv.Start(); err != nil {
		t.Fatalf("start wasmtime serve: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
		if t.Failed() {
			t.Logf("wasmtime serve output:\n%s", log.String())
		}
	})
	return addr
}
