package e2eselfhost

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A connected socket is not evidence that input is readable. Keep the peer
// open without sending bytes, so only the receive deadline can finish.
func TestSelfHostWasmSocketReadDeadline(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		t.Run(compiler.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				if conn, err := listener.Accept(); err == nil {
					defer conn.Close()
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "deadline.fern"), filepath.Join(dir, "deadline.wasm")
			program := fmt.Sprintf(`import "std/tcp";
import "std/time";
function main(): i32 {
    var fd: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    assert(fd >= 0);
    match (tcp.tcp_recv_deadline(fd, 1, time.duration_millis(10i64))) {
        None => {},
        Some(_) => { assert(false); }
    }
    assert(tcp_close(fd) == 0);
    return 0;
}
`, listener.Addr().(*net.TCPAddr).Port)
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-target", "wasm32-wasi", "-o", bin}
			if compiler.name == "primary" {
				args = append(args, "-emit", "core-module")
			}
			args = append(args, src)
			if compiler.name == "primary" {
				args = append(args, stdlib)
			}
			if out, err := exec.Command(compiler.path, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := exec.Command("wasmtime", "run", "-S", "inherit-network", bin)
			if compiler.name == "primary" {
				run = composeSelfHostWat(t, bin)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, run.Path, run.Args[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("receive deadline: %v\n%s", err, out)
			}
		})
	}
}
