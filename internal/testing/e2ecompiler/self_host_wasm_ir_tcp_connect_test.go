package e2ecompiler

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmIRTcpConnect(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	dir := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	openPort := ln.Addr().(*net.TCPAddr).Port

	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (for closed port): %v", err)
	}
	closedPort := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()

	run := func(t *testing.T, name, src string) string {
		t.Helper()
		compPath := selfHostSocketComponent(t, dir, name, src, "call $__fern_tcp_connect_with")
		out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath).Output()
		if err != nil {
			t.Fatalf("wasmtime run: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	t.Run("connects_to_open_port", func(t *testing.T) {
		src := fmt.Sprintf(`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { write("refused\n"); return 0; }
    write("connected\n");
    tcp_close(c);
    return 0;
}`, openPort)
		if got := run(t, "tcp_open", src); got != "connected" {
			t.Errorf("stdout = %q, want %q", got, "connected")
		}
	})

	t.Run("refused_on_closed_port_returns_negative", func(t *testing.T) {
		src := fmt.Sprintf(`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { write("refused\n"); return 0; }
    write("connected\n");
    tcp_close(c);
    return 0;
}`, closedPort)
		if got := run(t, "tcp_closed", src); got != "refused" {
			t.Errorf("stdout = %q, want %q — connect to an unbound port must return -errno", got, "refused")
		}
	})
}

// selfHostSocketComponent compiles src with the self-host CLI into the
// default wasm32-wasi component, the composition users get. The WAT of the
// same source must call each of wantSyms, so a module off the IR path fails.
func selfHostSocketComponent(t *testing.T, dir, name, src string, wantSyms ...string) string {
	t.Helper()
	cli, stdlib := e2eharness.SelfHostCLI(t), e2eharness.SelfHostStdlibRoot(t)
	srcPath := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	watPath := filepath.Join(dir, name+".wat")
	if out, err := exec.Command(cli, "-target", "wasm32-wasi", "-emit", "asm", "-o", watPath, srcPath, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("%s: compile to WAT: %v\n%s", name, err, out)
	}
	wat, err := os.ReadFile(watPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range wantSyms {
		if !strings.Contains(string(wat), sym) {
			t.Fatalf("%s: emitted WAT has no `%s` (module bailed off the IR path?)", name, sym)
		}
	}
	compPath := filepath.Join(dir, name+".wasm")
	if out, err := exec.Command(cli, "-target", "wasm32-wasi", "-o", compPath, srcPath, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("%s: compile to a component: %v\n%s", name, err, out)
	}
	return compPath
}
