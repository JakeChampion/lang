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

// httpHandlerSrc is the flagship edge-handler program: std/http + std/tcp + a
// `handle` function. Its merged stdlib closure is ~925 functions, so it is the
// canonical over-budget program — the shape the per-module IR rescue exists for.
//
// (main is explicit because the self-host checker has no `handle`-only auto-main
// synthesis; native's synthesiseHandleMain is checker-side and native-only.)
const httpHandlerSrc = `import "std/http";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("method=" + req.method + " path=" + req.path);
}

function main(): i32 {
    return tcp.tcp_serve(8080, handle);
}
`

// TestSelfHostHttpHandlerRoutesIRX86_64 pins the routing and the LINKABILITY of
// the flagship program through the file-based self-host driver.
//
// Before this change it compiled to assembly carrying a dozen dangling `__fn_`
// symbols — `tcp_listen`, `tcp_send`, `poll`, `tcp_pollable`, … — and failed at
// link, because the merged closure crosses the 512-function IR budget and the
// file-based driver had no over-budget rescue, so the whole program dropped to
// the AST emitter, which cannot emit those builtins at all. Every one of its
// functions lowers on the IR path, so this is a routing gate.
//
// Linking is the assertion that matters here: it is what catches a unit whose
// runtime helper the entry never emitted (the all_runtime_need_roots gap) and a
// shape symbol defined twice in one stream (the `.weak` merge only happens
// across separate object files, not within one).
//
// TestSelfHostHttpHandlerServesX86_64 below takes it the rest of the way and
// serves real requests through it.
func TestSelfHostHttpHandlerRoutesIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	asm, progDir := compileSourceModload(t, runner, driverBin, httpHandlerSrc)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the HTTP handler program")
	}
	if !strings.Contains(asm, ".Lir") {
		t.Fatal("no .Lir labels — the program did not lower through the IR")
	}
	// buildBin fails the test on a link error, which is the point: an undefined
	// __fern_* or a doubly-defined __fern_shp_* both surface here.
	buildBin(t, gcc, progDir, "http_handler", asm)
}

// TestSelfHostOverBudgetRoutesIRArm64 pins that `-target arm64-linux` reaches the IR
// path for the same over-budget programs, so the arm64 half of #3457 does NOT
// need the per-module concat.
//
// Worth pinning because the shape invites the opposite conclusion: the driver's
// arm64 branch returns `asm_arm64.emit_module` directly, with none of the
// budget arithmetic or concat rescue the x86 branch below carries, which reads
// like a branch that cannot route IR at all. It is not — `emit_module`
// tries `all_eligible` first, and unlike x86 the arm64 merged IR path has no
// 512-function budget, so it takes programs the x86 path has to rescue.
// (It emits the whole closure rather than the treeshaken subset, so its output
// is several times larger; that is a size difference, not a routing one.)
//
// Only the x86-built driver is needed — asserting the ROUTING reads the emitted
// assembly, so no aarch64 cross toolchain is involved.
func TestSelfHostOverBudgetRoutesIRArm64(t *testing.T) {
	_, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range []struct{ name, src string }{
		{"http-handler", httpHandlerSrc},
		{"http-parse", `import "std/http";
function main(): i32 {
    var raw: string = "GET /abc HTTP/1.1\r\nHost: x\r\n\r\n";
    match (http.http_parse_request(raw)) {
        Some(req) => { return req.path.len(); },
        None => { return 7; }
    }
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm, _ := compileSourceModload(t, runner, driverBin, tc.src, "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes for -target arm64")
			}
			if !strings.Contains(asm, ".Lir") {
				t.Error("did not lower through the arm64 IR path, which " +
					"carries no function budget, so an over-budget program should still " +
					"reach it (#3457)")
			}
		})
	}
}

// TestSelfHostOverBudgetProgramsRunX86_64 pins the per-module rescue on two
// programs that pull in the same ~925-function stdlib closure:
//
//   - http-parse calls no builtin the AST emitter is missing. Holding it back
//     from the concat by a gate hides two IR-path over-frees (the
//     container-read alias and the param-receiver
//     append, both fixed). The gate is gone, so it now takes the concat like
//     every other over-budget program — and must still run correctly.
//   - raw-socket-serve drives a real socket, which the AST emitter cannot emit
//     at all, so it takes the concat — and serves.
func TestSelfHostOverBudgetProgramsRunX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	t.Run("http-parse", func(t *testing.T) {
		src := `import "std/http";
function main(): i32 {
    var raw: string = "GET /abc HTTP/1.1\r\nHost: x\r\n\r\n";
    match (http.http_parse_request(raw)) {
        Some(req) => {
            print("method=" + req.method + " path=" + req.path);
            return req.path.len();
        },
        None => { return 7; }
    }
}
`
		asm, progDir := compileSourceModload(t, runner, driverBin, src)
		if !strings.Contains(asm, ".Lir") {
			t.Error("http-parse did not lower through the IR: with the rescue's gate removed, " +
				"every over-budget program in the window routes per-module IR (#3457)")
		}
		bin := buildBin(t, gcc, progDir, "http_parse", asm)
		out, exit := runBin(binCmd(runner, bin), "")
		if exit != 4 {
			t.Errorf("exit = %d, want 4 (len(\"/abc\"))", exit)
		}
		if !strings.Contains(out, "method=GET path=/abc") {
			t.Errorf("stdout = %q, want it to contain %q", out, "method=GET path=/abc")
		}
	})

	t.Run("raw-socket-serve", func(t *testing.T) {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("no free TCP port: %v", err)
		}
		port := probe.Addr().(*net.TCPAddr).Port
		probe.Close()

		// std/http is imported to pull in the over-budget closure; the socket
		// work uses the builtins directly, so no fn value crosses a unit
		// boundary — the serve-loop shape is covered by TestSelfHostHttpHandlerServes.
		src := fmt.Sprintf(`import "std/http";
function main(): i32 {
    var fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    var c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    var req: u8[] = tcp_recv(c, 4096);
    if (req.len() == 0) { return 93; }
    var n: i32 = tcp_send(c, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nrawok");
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}
`, port)
		asm, progDir := compileSourceModload(t, runner, driverBin, src)
		if !strings.Contains(asm, ".Lir") {
			t.Fatal("raw-socket program did not route through the IR path")
		}
		bin := buildBin(t, gcc, progDir, "raw_socket", asm)

		cmd := binCmd(runner, bin)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start server: %v", err)
		}
		defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

		addr := fmt.Sprintf("127.0.0.1:%d", port)
		var resp string
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if derr != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
			buf := make([]byte, 256)
			n, _ := bufio.NewReader(conn).Read(buf)
			resp = string(buf[:n])
			conn.Close()
			break
		}
		if !strings.Contains(resp, "rawok") {
			t.Errorf("response = %q, want it to contain %q", resp, "rawok")
		}
		_ = cmd.Wait()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("server exited %d, want 42", code)
		}
	})
}

// TestSelfHostHttpHandlerServesX86_64 is the end of the line for the flagship
// program: compiled by the SELF-HOSTED compiler, it answers real HTTP.
//
// It is the whole edge-handler stack at once — std/tcp's accept/recv/deadline
// loop, std/http's request parse and response serialise, the builtin
// HttpRequest / HttpResponse / Platform layouts, and the handler itself reached
// as a fn VALUE handed across a per-module unit boundary (#5698). Two requests
// on separate connections, because the second only answers correctly if the
// first request's allocations were reclaimed rather than leaked or reused.
func TestSelfHostHttpHandlerServesX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

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
	if !strings.Contains(asm, ".Lir") {
		t.Fatal("handler program did not route through the IR path")
	}
	bin := buildBin(t, gcc, progDir, "http_serve", asm)

	cmd := binCmd(runner, bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	get := func(path string) string {
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
			conn.Close()
			return string(buf[:n])
		}
		return ""
	}
	for _, want := range []string{"method=GET path=/hello", "method=GET path=/second"} {
		path := "/hello"
		if strings.HasSuffix(want, "/second") {
			path = "/second"
		}
		if resp := get(path); !strings.Contains(resp, want) {
			t.Errorf("GET %s response = %q, want it to contain %q", path, resp, want)
		}
	}
	// The server must still be running: tcp_serve loops, and a crash mid-request
	// is exactly the #5698 failure this pins.
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Errorf("server exited (code %d) instead of continuing to serve", cmd.ProcessState.ExitCode())
	}
}

func binCmd(runner []string, bin string) *exec.Cmd {
	if len(runner) == 0 {
		return exec.Command(bin)
	}
	return exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
}
