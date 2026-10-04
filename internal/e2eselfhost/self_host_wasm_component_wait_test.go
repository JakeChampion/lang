package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
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

	t.Run("sleep", func(t *testing.T) {
		comp := component(t, "sleep", `function main(): i32 {
    sleep_ms(80 as i64);
    sleep_ns(1000000 as i64);
    print("ok");
    return 0;
}
`)
		start := time.Now()
		out, err := exec.Command("wasmtime", "run", comp).CombinedOutput()
		if err != nil {
			t.Fatalf("wasmtime run: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "ok" {
			t.Fatalf("stdout %q, want \"ok\"", got)
		}
		if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
			t.Fatalf("the component ran %v, less than the 80 ms it sleeps", elapsed)
		}
	})

	// poll over an empty set with no timeout is the program native's
	// TestPollEmptySetInterpWasm builds: no other I/O, so the no-I/O framing
	// used to refuse it. Its verdict is whatever native's component reports.
	t.Run("poll_empty_set", func(t *testing.T) {
		const src = `function main(): i32 {
    let fds: i32[] = [];
    return poll(fds, 0);
}
`
		comp := component(t, "poll", src)
		got := exec.Command("wasmtime", "run", comp)
		gotOut, _ := got.CombinedOutput()
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
		want := exec.Command("wasmtime", "run", ncomp)
		wantOut, _ := want.CombinedOutput()
		if got.ProcessState.ExitCode() != want.ProcessState.ExitCode() {
			t.Fatalf("self-host component exit %d (%s), native's %d (%s)", got.ProcessState.ExitCode(), gotOut, want.ProcessState.ExitCode(), wantOut)
		}
	})

	// The probes native's wasm legs run, each printing "ok" or its first
	// failing check: the reactor floor (reactor_new / reactor_ctl /
	// reactor_wait), the raw socket controls (sleep_ms over loopback) and the
	// datagram sockets (udp_sendto with a u8[] payload).
	for _, p := range []struct {
		name string
		src  func() string
	}{
		{"reactor_floor", e2eharness.ReactorProbe},
		{"socket_ctl", e2eharness.SocketCtlProbe},
		{"udp", e2eharness.UdpSocketProbe},
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
