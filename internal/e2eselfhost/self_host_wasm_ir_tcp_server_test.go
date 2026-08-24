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
	"time"
)

// TestSelfHostWasmIRTcpServer pins the TCP server half on the self-host wasm-IR
// path — slice 3 of #4318, completing the set: tcp_listen, tcp_accept,
// tcp_pollable.
//
// The guest is the SERVER here and a host client connects INTO it, which is the
// only way to exercise accept for real. Both subtests therefore run the
// component in the background and drive it from Go.
//
// Three things worth knowing about these helpers:
//
//   - accept is non-blocking on wasi:sockets, so tcp_accept subscribes and
//     blocks on the socket's pollable first; calling accept straight away just
//     yields would-block.
//   - accept's result is the widest in the file —
//     result<tuple<tcp-socket, input-stream, output-stream>, error-code> — laid
//     out disc@+0, 3 pad, then the three handles at +4/+8/+12, hence a 16-byte
//     retptr where connect's tuple of two needed only 12.
//   - a LISTENER struct leaves its stream slots zero (it has no streams until it
//     accepts). tcp_close already guards each drop on a non-zero handle for
//     exactly this case, since passing 0 to a resource-drop import traps
//     host-side — so the same tcp_close serves listeners and connections.
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	witDir, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	// freePort reserves a port and releases it, so the guest can bind it.
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

	// compile emits the WAT, embeds the world, composes and validates.
	compile := func(t *testing.T, name, src string, wantSyms []string) string {
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

	// serveAndDial runs the guest server, dials it once from the host with
	// `send`, and returns (guest stdout, what the host read back).
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

	// The guest binds a port chosen here, so the port has to be baked into the
	// source; portsByComp carries it from compile time to dial time.
	t.Run("listen_accept_echo", func(t *testing.T) {
		port := freePort(t)
		src := fmt.Sprintf(`function main(): i32 {
    var l: i32 = tcp_listen(%d);
    if (l < 0) { write("listen-failed\n"); return 1; }
    write("listening\n");
    var c: i32 = tcp_accept(l);
    if (c < 0) { write("accept-failed\n"); return 1; }
    var r: string = string_from_bytes_unchecked(tcp_recv(c, 64));
    write("recv="); write(r);
    var n: i32 = tcp_send(c, "pong");
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

	// tcp_pollable feeds the wasm_poll multiplexer from #4316 — the composition
	// std/async relies on. Polling a connection with data pending must report
	// index 0 ready, and the subsequent recv must still see the bytes.
	t.Run("pollable_feeds_wasm_poll", func(t *testing.T) {
		port := freePort(t)
		src := fmt.Sprintf(`function main(): i32 {
    var l: i32 = tcp_listen(%d);
    if (l < 0) { write("listen-failed\n"); return 1; }
    write("listening\n");
    var c: i32 = tcp_accept(l);
    if (c < 0) { write("accept-failed\n"); return 1; }
    var p: i32 = tcp_pollable(c);
    var ps: i32[] = [p];
    var i: i32 = wasm_poll(ps);
    write("polled="); print_int(i);
    var r: string = string_from_bytes_unchecked(tcp_recv(c, 64));
    write(" recv="); write(r); write("\n");
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

// compPorts maps a compiled component path to the port its guest binds, so the
// host client knows where to dial. The port is baked into the guest source at
// compile time, so it cannot be discovered from the running process.
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
