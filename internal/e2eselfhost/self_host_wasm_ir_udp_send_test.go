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

// TestSelfHostWasmIRUdpSend pins udp_send on the self-host wasm-IR path —
// #4375 item 3, and the last socket gap after #4318 closed the tcp_* set.
//
// `udp_send(host, port, data)` is one-shot fire-and-forget: bind an ephemeral
// local port, connect a stream to host:port, wait for a send permit, send one
// datagram, tear it all down. Two things differ from the tcp helpers:
//
//   - The host is an IPv4 LITERAL STRING ("a.b.c.d"), not a packed i32 like
//     tcp_connect's, so the backend parses the dotted quad ($__fern_ipv4_parse).
//     A malformed literal returns -1 without touching the network, which the
//     reject cases below pin — a parser that fell through to the socket calls
//     would produce a confusing errno instead.
//   - `send` takes list<outgoing-datagram>, a record of list<u8> +
//     option<ip-socket-address>. Connecting via stream(Some(remote)) puts the
//     destination in the 15-i32 ip-socket-address flattening, so the datagram
//     carries remote-address: NONE and only its data ptr/len and the option
//     discriminant need writing — no hand-marshalling of the address variant.
//
// The delivery case asserts the datagram ARRIVES at a real host UDP socket, not
// just that the guest returned a plausible number: UDP is connectionless, so a
// send to nowhere also "succeeds" (see sends_without_listener below). Only the
// receiving end proves the payload was marshalled correctly.
func TestSelfHostWasmIRUdpSend(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR udp-send e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR udp-send e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR udp-send e2e")
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
		if !strings.Contains(string(wat), "call $__fern_udp_send") {
			t.Fatalf("%s: emitted WAT has no `call $__fern_udp_send` (module bailed off the IR path?)", name)
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

	t.Run("datagram_reaches_a_real_listener", func(t *testing.T) {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen udp: %v", err)
		}
		defer pc.Close()
		port := pc.LocalAddr().(*net.UDPAddr).Port

		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			_ = pc.SetReadDeadline(time.Now().Add(30 * time.Second))
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				got <- ""
				return
			}
			got <- string(buf[:n])
		}()

		out := run(t, "udp_ok", fmt.Sprintf(`function main(): i32 {
    var n: i32 = udp_send("127.0.0.1", %d, "hello-udp");
    write("sent="); print_int(n); write("\n");
    return 0;
}`, port))
		if out != "sent=9" {
			t.Errorf("guest stdout = %q, want %q", out, "sent=9")
		}
		select {
		case payload := <-got:
			if payload != "hello-udp" {
				t.Errorf("host received %q, want %q", payload, "hello-udp")
			}
		case <-time.After(30 * time.Second):
			t.Error("host UDP socket received nothing")
		}
	})

	// A malformed IPv4 literal must be rejected by the parser BEFORE any socket
	// call, returning -1. Falling through to the socket path would surface some
	// unrelated errno instead.
	for _, tc := range []struct{ name, host string }{
		{"rejects_non_numeric_host", "not-an-ip"},
		{"rejects_too_few_octets", "1.2.3"},
		{"rejects_octet_over_255", "1.2.3.999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, tc.name, fmt.Sprintf(`function main(): i32 {
    var n: i32 = udp_send("%s", 9311, "x");
    write("r="); print_int(n); write("\n");
    return 0;
}`, tc.host))
			if out != "r=-1" {
				t.Errorf("guest stdout = %q, want %q for host %q", out, "r=-1", tc.host)
			}
		})
	}

	// UDP is connectionless: a well-formed send to a port nobody is listening on
	// still reports the bytes accepted by the host stack. Pinned so the
	// delivery case above is understood to be the ONLY one that proves the
	// payload actually went out correctly.
	t.Run("sends_without_listener", func(t *testing.T) {
		out := run(t, "udp_nolisten", `function main(): i32 {
    var n: i32 = udp_send("127.0.0.1", 9399, "xyz");
    write("r="); print_int(n); write("\n");
    return 0;
}`)
		if out != "r=3" {
			t.Errorf("guest stdout = %q, want %q", out, "r=3")
		}
	})
}
