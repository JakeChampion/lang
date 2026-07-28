package e2eselfhost

import (
	"bufio"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSelfHostHttpHandlerX86_64 is the self-host mirror of internal/e2e's
// TestX86_64HttpHandler: the flagship edge-handler program — `std/http` +
// `std/tcp` + a `handle` function — compiled by the SELF-HOSTED compiler,
// linked, and serving a real HTTP request over a real socket.
//
// It could not be built at all before #5686 and this change. Three things had
// to line up, and each failed far from its cause:
//
//  1. `proc_fork` / `proc_waitpid` had no self-host lowering, so std/tcp
//     emitted `call __fn_proc_fork` against nothing (#5686).
//  2. `Platform` was not a declared builtin struct self-host, so every
//     `(HttpRequest, Platform) => HttpResponse` signature failed to resolve
//     and `__serve_loop` bailed the IR path.
//  3. The merged stdlib closure (~925 functions) overflows the 512-function
//     merged-IR budget, and the file-based driver had no over-budget rescue —
//     so the whole program dropped to the AST emitter, which cannot emit
//     `tcp_listen` / `poll` / `tcp_pollable` at all. The result was asm with a
//     dozen dangling `__fn_` symbols and a link failure, even though every
//     function in the program lowers on the IR path.
//
// Serving one real request is what pins all three at once: a regression in any
// of them shows up as a link failure or a dead socket, not a subtle diff.
func TestSelfHostHttpHandlerX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// The self-host checker has no `handle`-only auto-main synthesis (native's
	// synthesiseHandleMain is checker-side and native-only), so main is
	// explicit. Everything else is the canonical handler shape.
	src := fmt.Sprintf(`import "std/http";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("method=" + req.method + " path=" + req.path);
}

function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}
`, port)

	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the HTTP handler program")
	}
	// The per-module IR rescue is what makes this linkable; the AST emitter has
	// no tcp_listen / poll body, so an AST-routed build dangles at link. Assert
	// the routing directly so a regression names itself instead of surfacing as
	// an opaque linker error.
	if !strings.Contains(asm, ".Lir") {
		t.Error("emitted asm has no .Lir labels — the program routed through the AST emitter, which cannot emit tcp_listen/poll")
	}
	bin := buildBin(t, gcc, progDir, "http_handler", asm)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// Two requests on separate connections: the second one is the assertion
	// that the first request's allocations were reclaimed inside tcp_serve
	// (a leak there scrambles state or dies), the same property the native
	// TestX86_64HttpHandler checks.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i, path := range []string{"/hello", "/second"} {
		var resp string
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if derr != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: x\r\n\r\n", path)
			buf := make([]byte, 512)
			n, _ := bufio.NewReader(conn).Read(buf)
			resp = string(buf[:n])
			conn.Close()
			break
		}
		want := "method=GET path=" + path
		if !strings.Contains(resp, want) {
			t.Fatalf("request %d: response = %q, want it to contain %q", i+1, resp, want)
		}
		if !strings.Contains(resp, "200") {
			t.Errorf("request %d: response = %q, want a 200 status line", i+1, resp)
		}
	}
}
