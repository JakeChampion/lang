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
	"time"
)

func TestSelfHostWasmIRTcpServer(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR tcp-server e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR tcp-server e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR tcp-server e2e")
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

	freePort := func(t *testing.T) int {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve port: %v", err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return p
	}

	compile := func(t *testing.T, name, src string, wantSyms []string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(withPrintInt(src)))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		for _, sym := range wantSyms {
			if !strings.Contains(string(wat), sym) {
				t.Fatalf("%s: emitted WAT has no `%s` (module bailed off the IR path?)", name, sym)
			}
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
		return compPath
	}

	serveAndDial := func(t *testing.T, compPath, send string) (string, string) {
		t.Helper()
		cmd := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Start(); err != nil {
			t.Fatalf("start guest: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		var reply string
		var dialErr error
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%s", portOf(t, compPath)), 500*time.Millisecond)
			if err != nil {
				dialErr = err
				time.Sleep(200 * time.Millisecond)
				continue
			}
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := c.Write([]byte(send)); err != nil {
				c.Close()
				dialErr = err
				break
			}
			buf := make([]byte, 256)
			n, _ := c.Read(buf)
			reply = string(buf[:n])
			c.Close()
			dialErr = nil
			break
		}
		if dialErr != nil {
			cmd.Process.Kill()
			t.Fatalf("host client could not reach the guest server: %v", dialErr)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("guest exited with error: %v\nstdout:\n%s", err, stdout.String())
			}
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
			t.Fatalf("guest did not exit; stdout:\n%s", stdout.String())
		}
		return strings.TrimSpace(stdout.String()), reply
	}

	t.Run("listen_accept_echo", func(t *testing.T) {
		port := freePort(t)
		src := fmt.Sprintf(wasmRecvHelper+`function main(): i32 {
    let l: i32 = tcp_listen(%d);
    if (l < 0) { write("listen-failed\n"); return 1; }
    write("listening\n");
    let c: i32 = tcp_accept(l);
    if (c < 0) { write("accept-failed\n"); return 1; }
    let r: string = recv(c, 64);
    write("recv="); write(r);
    let n: i32 = tcp_send_bytes(c, [112u8, 111u8, 110u8, 103u8]);
    write(" sent="); print_int(n); write("\n");
    tcp_close(c);
    tcp_close(l);
    return 0;
}`, port)
		comp := compile(t, "tcp_server", src,
			[]string{"call $__fern_tcp_listen", "call $__fern_tcp_accept"})
		rememberPort(comp, port)
		out, reply := serveAndDial(t, comp, "ping")
		if want := "listening\nrecv=ping sent=4"; out != want {
			t.Errorf("guest stdout = %q, want %q", out, want)
		}
		if reply != "pong" {
			t.Errorf("host client read %q, want %q", reply, "pong")
		}
	})

	t.Run("local_port_of_ephemeral_listener", func(t *testing.T) {
		src := `function main(): i32 {
    let l: i32 = tcp_listen(0);
    if (l < 0) { write("listen-failed\n"); return 1; }
    let port: i32 = tcp_local_port(l);
    tcp_close(l);
    if (port <= 0) { write("no-port\n"); return 1; }
    if (port > 65535) { write("bad-port\n"); return 1; }
    write("port-in-range\n");
    return 0;
}`
		comp := compile(t, "tcp_local_port", src,
			[]string{"call $__fern_tcp_local_port", "tcp-socket.local-address"})
		out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", comp).Output()
		if err != nil {
			t.Fatalf("guest exited with error: %v\nstdout:\n%s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "port-in-range" {
			t.Errorf("guest stdout = %q, want %q", got, "port-in-range")
		}
	})

	t.Run("pollable_feeds_wasm_poll", func(t *testing.T) {
		port := freePort(t)
		src := fmt.Sprintf(`function main(): i32 {
    let l: i32 = tcp_listen(%d);
    if (l < 0) { write("listen-failed\n"); return 1; }
    write("listening\n");
    let c: i32 = tcp_accept(l);
    if (c < 0) { write("accept-failed\n"); return 1; }
    let p: i32 = tcp_pollable(c);
    let ps: i32[] = [p];
    let i: i32 = wasm_poll(ps);
    write("polled="); print_int(i);
    let buf: u8[] = [0u8, 0u8, 0u8, 0u8];
    let n: i32 = tcp_recv_into(c, buf);
    write(" recv="); if (n == 4) { write(string_from_bytes_unchecked(buf)); } write("\n");
    wasm_pollable_drop(p);
    tcp_close(c);
    tcp_close(l);
    return 0;
}`, port)
		comp := compile(t, "tcp_pollable", src,
			[]string{"call $__fern_tcp_pollable", "call $__fern_wasm_poll"})
		rememberPort(comp, port)
		out, _ := serveAndDial(t, comp, "data")
		if want := "listening\npolled=0 recv=data"; out != want {
			t.Errorf("guest stdout = %q, want %q", out, want)
		}
	})
}

var compPorts = map[string]int{}

func rememberPort(compPath string, port int) { compPorts[compPath] = port }

func portOf(t *testing.T, compPath string) string {
	t.Helper()
	p, ok := compPorts[compPath]
	if !ok {
		t.Fatalf("no port recorded for %s", compPath)
	}
	return fmt.Sprintf("%d", p)
}
