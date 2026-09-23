package e2eharness

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// HTTPHandlerCensusSource bounds the production accept loop without replacing
// its read, parse, handler or serialization path. The parent owns fd 3.
func HTTPHandlerCensusSource(t *testing.T, root string, rounds int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "internal/stdlib/std/tcp.fern"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "function __serve_loop(")
	if start < 0 {
		t.Fatal("production accept loop not found")
	}
	end := strings.Index(src[start:], "\n// __serve_loop_with")
	if end < 0 {
		t.Fatal("production accept loop boundary not found")
	}
	end += start
	body := src[start:end]
	if strings.Count(body, "while (true)") != 1 || strings.Count(body, "tcp_close(fd);") != 1 {
		t.Fatal("accept loop changed; update its bounded census fixture")
	}
	body = strings.Replace(body, "while (true)", fmt.Sprintf("var completed: i32 = 0;\n    while (completed < %d)", rounds), 1)
	body = strings.Replace(body, "tcp_close(fd);", "tcp_close(fd);\n        completed = completed + 1;", 1)
	return src[:start] + body + src[end:] + `
function census_handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}
function main(): i32 {
    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), 10000);
}
`
}

func RunHTTPHandlerCensus(t *testing.T, command *exec.Cmd, rounds int) string {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	cmd.Env, cmd.Dir = command.Env, command.Dir
	cmd.ExtraFiles = append(cmd.ExtraFiles, file)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
			t.Logf("server output: %s", &out)
		}
	}()
	httpHandlerCensusRequests(t, listener.Addr().String(), rounds)
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("bounded HTTP server: %v\n%s", err, &out)
	}
	return out.String()
}

func httpHandlerCensusRequests(t *testing.T, addr string, rounds int) {
	t.Helper()
	for i := 0; i < rounds; i++ {
		conn, err := net.DialTimeout("tcp4", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		err = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err == nil {
			_, err = io.WriteString(conn, "GET /hello HTTP/1.1\r\nHost: localhost\r\n\r\n")
		}
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			conn.Close()
			t.Fatalf("request %d: %v", i, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if err != nil || resp.StatusCode != 200 || resp.Proto != "HTTP/1.1" || resp.ContentLength != 2 || string(body) != "ok" {
			t.Fatalf("request %d: status=%d proto=%s length=%d body=%q error=%v", i, resp.StatusCode, resp.Proto, resp.ContentLength, body, err)
		}
	}
}

func WasiHTTPHandlerCensusSource(t *testing.T, root string, rounds int) string {
	t.Helper()
	src := HTTPHandlerCensusSource(t, root, rounds)
	const original = "    return __serve_loop(3, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), 10000);"
	if strings.Count(src, original) != 1 {
		t.Fatal("bounded HTTP entry changed")
	}
	const entry = `    var listener: i32 = tcp_listen(0);
    if (listener < 0) { return 90; }
    var port: i32 = tcp_local_port(listener);
    if (port <= 0) { return 91; }
    print(int.int_to_string(port));
    var result: i32 = __serve_loop(listener, (req: HttpRequest, plat: Platform): HttpResponse => census_handle(req, plat), 10000);
    if (tcp_close(listener) != 0) { return 92; }
    return result;`
	return strings.Replace(src, original, entry, 1)
}

func RunWasiHTTPHandlerCensus(t *testing.T, component string, rounds int) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wasmtime", "run", "-S", "inherit-network", component)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
			t.Logf("server stderr: %s", &stderr)
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port <= 0 || port > 65535 {
		t.Fatalf("invalid server port %q: %v", line, err)
	}
	httpHandlerCensusRequests(t, fmt.Sprintf("127.0.0.1:%d", port), rounds)
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("unexpected server stdout: %s", rest)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("bounded WASI server: %v\n%s", err, &stderr)
	}
	return stderr.String()
}
