package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The timer and pollable builtins in a wasi:cli/run component built by the
// self-host CLI (#11411): a sleep is a timer pollable held to completion,
// poll waits on wasi:io/poll, and the reactor and the datagram sockets reach
// the host the way native's component does. Each program used to be refused
// by the component framing, which had no preview-2 body for it.
func TestSelfHostWasmComponentWaits(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	cli := newStrictCLI(t)

	component := func(t *testing.T, name, src string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		comp := filepath.Join(dir, name+".component.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, path, cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component build: %v\n%s", err, out)
		}
		return comp
	}

	// Each sleep is timed inside the guest, on the monotonic clock, so the
	// host's startup cannot stand in for a sleep that returned at once.
	t.Run("sleep", func(t *testing.T) {
		comp := component(t, "sleep", `function main(): i32 {
    let t0: i64 = monotonic_ns();
    sleep_ms(80 as i64);
    let t1: i64 = monotonic_ns();
    sleep_ns(5000000 as i64);
    let t2: i64 = monotonic_ns();
    if (t1 - t0 < 80000000 as i64) { print("sleep_ms returned early"); return 1; }
    if (t2 - t1 < 5000000 as i64) { print("sleep_ns returned early"); return 2; }
    print("ok");
    return 0;
}
`)
		out, err := exec.Command("wasmtime", "run", comp).CombinedOutput()
		if got := strings.TrimSpace(string(out)); err != nil || got != "ok" {
			t.Fatalf("stdout %q (%v), want \"ok\"", got, err)
		}
	})

	// poll over an empty set with no timeout is the program native's
	// TestPollEmptySetInterpWasm builds, its only host contact the poll, so
	// the no-I/O framing used to refuse it. Its verdict is printed rather
	// than returned: a run export collapses any non-zero main to exit 1,
	// which a component that never instantiated would match.
	t.Run("poll_empty_set", func(t *testing.T) {
		const src = `import "core/int";

function main(): i32 {
    let fds: i32[] = [];
    print(int.int_to_string(poll(fds, 0)));
    return 0;
}
`
		comp := component(t, "poll", src)
		gotOut, gerr := exec.Command("wasmtime", "run", comp).CombinedOutput()
		native := buildFernCLIBin(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "poll.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		ncomp := filepath.Join(dir, "poll.native.wasm")
		if out, err := exec.Command(native, "-target", "wasm32-wasi", "-o", ncomp, path).CombinedOutput(); err != nil {
			t.Fatalf("native component build: %v\n%s", err, out)
		}
		wantOut, werr := exec.Command("wasmtime", "run", ncomp).CombinedOutput()
		if werr != nil || strings.TrimSpace(string(wantOut)) == "" {
			t.Fatalf("native's component printed no verdict: %v\n%s", werr, wantOut)
		}
		if gerr != nil || string(gotOut) != string(wantOut) {
			t.Fatalf("self-host component printed %q (%v), native's %q", gotOut, gerr, wantOut)
		}
	})

	// The probes native's wasm legs run, each printing "ok" or its first
	// failing check: the reactor floor (reactor_new / reactor_ctl /
	// reactor_wait), the raw socket controls (sleep_ms over loopback), the
	// datagram sockets, and std/net's datagram faces, whose u8[] payloads
	// reach udp_send_bytes and udp_sendto_bytes. tcpBytesProbe covers the
	// third typed variant, tcp_send_bytes.
	for _, p := range []struct {
		name string
		src  func() string
	}{
		{"reactor_floor", e2eharness.ReactorProbe},
		{"socket_ctl", e2eharness.SocketCtlProbe},
		{"udp", e2eharness.UdpSocketProbe},
		{"net_udp", e2eharness.NetUdpProbe},
		{"tcp_bytes", tcpBytesProbe},
	} {
		t.Run(p.name, func(t *testing.T) {
			comp := component(t, p.name, p.src())
			out, err := exec.Command("wasmtime", "run", "-S", "inherit-network", comp).CombinedOutput()
			if _, exited := err.(*exec.ExitError); err != nil && !exited {
				t.Fatalf("wasmtime run: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "ok" {
				t.Fatalf("want \"ok\" on stdout; first failing check: %s", got)
			}
		})
	}
}

// tcpBytesProbe sends a u8[] over a loopback connection with tcp_send_bytes
// and reads it back on the accepted side, printing "ok" or the number of the
// first failing check. wasi:sockets never blocks, so the accept and the read
// retry until the peer's side has landed.
func tcpBytesProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function main(): i32 {
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: i32 = tcp_listen_with(any, 0, 4, false);
    if (ln < 0) { return fail(1); }
    let c: i32 = tcp_connect(16777343, tcp_local_port(ln));
    if (c < 0) { return fail(2); }
    let a: i32 = tcp_accept(ln);
    let tries: i32 = 0;
    while (a < 0 && tries < 200) {
        sleep_ms(5 as i64);
        a = tcp_accept(ln);
        tries = tries + 1;
    }
    if (a < 0) { return fail(3); }
    if (tcp_send_bytes(c, [98u8, 121u8, 116u8, 101u8, 115u8]) != 5) { return fail(4); }
    let buf: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
    let n: i32 = tcp_recv_into(a, buf);
    tries = 0;
    while (n < 0 && tries < 200) {
        sleep_ms(5 as i64);
        n = tcp_recv_into(a, buf);
        tries = tries + 1;
    }
    if (n != 5) { return fail(5); }
    if (buf[0] != 98u8 || buf[4] != 115u8) { return fail(6); }
    tcp_close(a);
    tcp_close(c);
    tcp_close(ln);
    print("ok");
    return 0;
}
`
}
