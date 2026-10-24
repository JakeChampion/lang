package e2ecompiler

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const httpHandlerSrc = `import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("method=" + req.method + " path=" + req.path);
}

function main(): i32 {
    return serve.run(8080, serve.config(), handle);
}
`

func TestSelfHostHttpHandlerRoutesIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	asm, progDir := compileSourceModload(t, runner, driverBin, httpHandlerSrc)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the HTTP handler program")
	}
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("no .Lssa_ labels — the program did not lower through the IR")
	}
	buildBin(t, gcc, progDir, "http_handler", asm)
}

func TestSelfHostOverBudgetRoutesIRArm64(t *testing.T) {
	_, runner, driverBin := buildModloadDriverX86(t)

	for _, tc := range []struct{ name, src string }{
		{"http-handler", httpHandlerSrc},
		{"http-parse", `import "std/http";
function main(): i32 {
    let raw: string = "GET /abc HTTP/1.1\r\nHost: x\r\n\r\n";
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
				t.Fatal("self-host compiler emitted 0 bytes for -target arm64-linux")
			}
			if !strings.Contains(asm, ".Lssa_") {
				t.Error("did not lower through the arm64 IR path, which an " +
					"over-budget program should reach through the per-module rescue (#3457)")
			}
		})
	}
}

func TestSelfHostOverBudgetProgramsRunX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	t.Run("http-parse", func(t *testing.T) {
		src := `import "std/http";
function main(): i32 {
    let raw: string = "GET /abc HTTP/1.1\r\nHost: x\r\n\r\n";
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
		if !strings.Contains(asm, ".Lssa_") {
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
		port := freeTCPPort(t)

		src := fmt.Sprintf(`import "std/http";
function main(): i32 {
    let fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    let c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    let req: u8[] = __alloc_u8(4096);
    if (tcp_recv_into(c, req) <= 0) { return 93; }
    let n: i32 = tcp_send_bytes(c, [72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 32u8, 50u8, 48u8, 48u8, 32u8, 79u8, 75u8, 13u8, 10u8, 67u8, 111u8, 110u8, 116u8, 101u8, 110u8, 116u8, 45u8, 76u8, 101u8, 110u8, 103u8, 116u8, 104u8, 58u8, 32u8, 53u8, 13u8, 10u8, 13u8, 10u8, 114u8, 97u8, 119u8, 111u8, 107u8]);
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}
`, port)
		asm, progDir := compileSourceModload(t, runner, driverBin, src)
		if !strings.Contains(asm, ".Lssa_") {
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

func TestSelfHostHttpHandlerServesX86_64(t *testing.T) {
	checkSelfHostHttpHandlerServes(t, func(port int) string {
		return fmt.Sprintf("return serve.run(%d, serve.config(), handle);", port)
	})
}

func TestSelfHostHttpHandlerServesWithOptionsX86_64(t *testing.T) {
	checkSelfHostHttpHandlerServes(t, func(port int) string {
		return fmt.Sprintf("let opts: serve.Config = serve.Config { ...serve.config(), backlog: 4, reuse_port: true };\n    return serve.run(%d, opts, handle);", port)
	})
}

func checkSelfHostHttpHandlerServes(t *testing.T, entry func(port int) string) {
	t.Helper()
	gcc, runner, driverBin := buildModloadDriverX86(t)
	port := freeTCPPort(t)
	asm, progDir := compileSourceModload(t, runner, driverBin, httpServeSrc(entry(port)))
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("handler program did not route through the IR path")
	}
	checkServes(t, binCmd(runner, buildBin(t, gcc, progDir, "http_serve", asm)), port)
}

func TestSelfHostHttpHandlerServesArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(httpServeSrc(fmt.Sprintf("return serve.run(%d, serve.config(), handle);", port))), 0o644); err != nil {
		t.Fatal(err)
	}
	checkServes(t, runArm64Bin(qemu, cli.arm64Binary(t, src)), port)
}

func httpServeSrc(mainBody string) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("method=" + req.method + " path=" + req.path);
}

function main(): i32 {
    %s
}
`, mainBody)
}

func checkServes(t *testing.T, cmd *exec.Cmd, port int) {
	t.Helper()
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
