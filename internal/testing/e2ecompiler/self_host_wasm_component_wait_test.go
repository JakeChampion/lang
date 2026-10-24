package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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

	for _, p := range []struct {
		name string
		src  func() string
	}{
		{"reactor_floor", e2eharness.ReactorProbe},
		{"socket_ctl", e2eharness.SocketCtlProbe},
		{"udp", e2eharness.UdpSocketProbe},
		{"net_udp", e2eharness.NetUdpProbe},
		{"tcp_bytes", tcpBytesProbe},
		{"reactor_grow", reactorGrowProbe},
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
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], tcp_local_port(ln), false);
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

func reactorGrowProbe() string {
	return `import "core/int";

function fail(n: i32): i32 {
    print(int.int_to_string(n));
    return n;
}

function index_of(fds: i32[], fd: i32): i32 {
    let i: i32 = 0;
    while (i < fds.len()) {
        if (fds[i] == fd) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}

// Waits until every descriptor in fds has been reported with bit, or 200
// waits pass; whether all were.
function all_report(r: i32, events: i32[], fds: i32[], bit: i32): boolean {
    let seen: i32[] = [];
    while (seen.len() < fds.len()) {
        seen = seen.append(0);
    }
    let left: i32 = fds.len();
    let waits: i32 = 0;
    while (left > 0 && waits < 200) {
        let n: i32 = reactor_wait(r, events, 2000);
        let j: i32 = 0;
        while (j < n) {
            let at: i32 = index_of(fds, events[j * 2]);
            if (at >= 0 && seen[at] == 0 && (events[j * 2 + 1] & bit) == bit) {
                seen = seen.with(at, 1);
                left = left - 1;
            }
            j = j + 1;
        }
        waits = waits + 1;
    }
    return left == 0;
}

function main(): i32 {
    let any: u8[] = [0u8, 0u8, 0u8, 0u8];
    let ln: i32 = tcp_listen_with(any, 0, 32, false);
    if (ln < 0) { return fail(1); }
    let port: i32 = tcp_local_port(ln);
    let r: i32 = reactor_new();
    if (r < 0) { return fail(2); }
    let cs: i32[] = [];
    let accs: i32[] = [];
    let all: i32[] = [];
    let i: i32 = 0;
    while (i < 20) {
        let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
        if (c < 0) { return fail(3); }
        let a: i32 = tcp_accept(ln);
        let tries: i32 = 0;
        while (a < 0 && tries < 200) {
            sleep_ms(5 as i64);
            a = tcp_accept(ln);
            tries = tries + 1;
        }
        if (a < 0) { return fail(4); }
        if (reactor_ctl(r, 1, c, 3) != 0 || reactor_ctl(r, 1, a, 3) != 0) { return fail(5); }
        cs = cs.append(c);
        accs = accs.append(a);
        all = all.append(c).append(a);
        i = i + 1;
    }
    let events: i32[] = [];
    while (events.len() < 128) {
        events = events.append(0);
    }
    if (!all_report(r, events, all, 2)) { return fail(6); }
    let before: i64 = __heap_alloc_count();
    let n: i32 = reactor_wait(r, events, 2000);
    let after: i64 = __heap_alloc_count();
    if (n < 1) { return fail(7); }
    if (after != before) { return fail(8); }
    i = 0;
    while (i < 20) {
        if (tcp_send_bytes(cs[i], [120u8]) != 1) { return fail(9); }
        i = i + 1;
    }
    if (!all_report(r, events, accs, 1)) { return fail(10); }
    let buf: u8[] = [0u8, 0u8, 0u8, 0u8];
    i = 0;
    while (i < 20) {
        if (tcp_recv_into(accs[i], buf) != 1 || buf[0] != 120u8) { return fail(11); }
        i = i + 1;
    }
    i = 0;
    while (i < 20) {
        if (reactor_ctl(r, 2, cs[i], 0) != 0 || reactor_ctl(r, 2, accs[i], 0) != 0) { return fail(12); }
        tcp_close(cs[i]);
        tcp_close(accs[i]);
        i = i + 1;
    }
    tcp_close(ln);
    if (reactor_ctl(r, 3, 0, 0) != 0) { return fail(13); }
    print("ok");
    return 0;
}
`
}
