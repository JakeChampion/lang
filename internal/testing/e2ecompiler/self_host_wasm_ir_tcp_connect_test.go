package e2ecompiler

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
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

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
		if !strings.Contains(string(wat), "call $__fern_tcp_connect_with") {
			t.Fatalf("%s: emitted WAT has no `call $__fern_tcp_connect_with` (module bailed off the IR path?)", name)
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
