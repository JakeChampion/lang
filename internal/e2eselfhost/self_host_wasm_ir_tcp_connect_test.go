package e2eselfhost

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIRTcpConnect pins the outbound TCP client on the self-host
// wasm-IR path — slice 1 of #4318: tcp_connect + tcp_close.
//
// wasm has no file descriptors, so the i32 the language calls a "fd" is a
// POINTER to a 12-byte heap struct, the same shape native uses
// (internal/codegen/wasmbin/wasi_tcp.go) so the two stay swappable:
//
//	[0..3]  tcp-socket handle
//	[4..7]  input-stream handle   (0 for listening sockets)
//	[8..11] output-stream handle  (0 for listening sockets)
//
// A negative return is -errno. tcp_close drops the streams BEFORE the socket:
// they are its children, and the canonical ABI rejects a parent drop while
// children live.
//
// Two things this needed beyond the helper itself:
//
//   - `start-connect` / `finish-connect` declared in cmd/fern/wit/deps/sockets/
//     tcp.wit. It was a server-only subset ("we lower Fern's narrower
//     listen+accept+stream pattern"), which is fine for native — it composes in
//     Go and never reads the WIT — but the self-host resolves imports through
//     `wasm-tools component embed`, so the compose failed without them.
//   - pollable.block / [resource-drop]pollable, which connect uses internally to
//     wait for the connection to establish, imported only when the module has
//     not already imported them for wasm_block / wasm_pollable_drop (a
//     duplicate import is an invalid module).
//
// BOTH directions are asserted. A helper that always returned a struct pointer
// would pass a connect-only test; the refused case is what proves the errno
// path, and it runs against a port deliberately left unbound.
func TestSelfHostWasmIRTcpConnect(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR tcp-connect e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR tcp-connect e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR tcp-connect e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	// A real listener for the success case. Its port is handed to the guest.
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

	// A port deliberately left unbound for the refused case: bind then close,
	// so nothing else grabs it in the meantime.
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen (for closed port): %v", err)
	}
	closedPort := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()

	run := func(t *testing.T, name, src string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		if !strings.Contains(string(wat), "call $__fern_tcp_connect") {
			t.Fatalf("%s: emitted WAT has no `call $__fern_tcp_connect` (module bailed off the IR path?)", name)
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, name+".core.wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		embedPath := filepath.Join(dir, name+".embed.wasm")
		if out, err := exec.Command(wasmtools, "component", "embed", witDir,
			"-w", "fern", corePath, "-o", embedPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
		}
		compPath := filepath.Join(dir, name+".component.wasm")
		if out, err := exec.Command(wasmtools, "component", "new", embedPath,
			"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component new: %v\n%s", err, out)
		}
		if out, err := exec.Command(wasmtools, "validate", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools validate: %v\n%s", err, out)
		}
		out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath).Output()
		if err != nil {
			t.Fatalf("wasmtime run: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	// 127.0.0.1 in the std/fetch host_be convention: a | b<<8 | c<<16 | d<<24.
	const localhostBE = "127 + (0 * 256) + (0 * 65536) + (1 * 16777216)"

	t.Run("connects_to_open_port", func(t *testing.T) {
		src := fmt.Sprintf(`function main(): i32 {
    var host: i32 = %s;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { write("refused\n"); return 0; }
    write("connected\n");
    tcp_close(c);
    return 0;
}`, localhostBE, openPort)
		if got := run(t, "tcp_open", src); got != "connected" {
			t.Errorf("stdout = %q, want %q", got, "connected")
		}
	})

	t.Run("refused_on_closed_port_returns_negative", func(t *testing.T) {
		src := fmt.Sprintf(`function main(): i32 {
    var host: i32 = %s;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { write("refused\n"); return 0; }
    write("connected\n");
    tcp_close(c);
    return 0;
}`, localhostBE, closedPort)
		if got := run(t, "tcp_closed", src); got != "refused" {
			t.Errorf("stdout = %q, want %q — connect to an unbound port must return -errno", got, "refused")
		}
	})
}
