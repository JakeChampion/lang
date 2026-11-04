package e2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var socketProbes = []struct {
	name string
	src  func() string
}{
	{"raw", e2eharness.SocketCtlProbe},
	{"send_queue", e2eharness.SendQueueProbe},
	{"peer_key", e2eharness.PeerKeyProbe},
	{"std_net", e2eharness.NetSocketOptsProbe},
	{"udp", e2eharness.UdpSocketProbe},
	{"udp_reuse", e2eharness.UdpReuseProbe},
	{"std_net_udp", e2eharness.NetUdpProbe},
	{"connect", e2eharness.ConnectProbe},
	{"std_net_connect", e2eharness.NetConnectProbe},
	{"relisten", e2eharness.RelistenProbe},
	{"read_reset", e2eharness.NetReadResetProbe},
}

func TestSocketCtlInterp(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := runInterpExitCode(t, p.src()); got != 42 {
				t.Fatalf("interp got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestSocketCtlX86_64(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunX86_64(t, p.src()); got != 42 {
				t.Fatalf("x86-64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestSocketCtlArm64(t *testing.T) {
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			if out, got := compileAndRunArm64(t, p.src()); got != 42 {
				t.Fatalf("arm64 got %d, want 42; first failing check: %s", got, out)
			}
		})
	}
}

func TestArm64DarwinSocketCtl(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(p.src()), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("arm64-darwin: %v, want exit 42; first failing check: %s", err, out)
			}
		})
	}
}

func wasmReadResetProbe(t *testing.T) string {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	t.Cleanup(func() {
		listener.Close()
		select {
		case err := <-done:
			if err != nil && !t.Failed() {
				t.Errorf("read-reset server: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("read-reset server did not stop")
		}
	})
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := listener.AcceptTCP()
			if err != nil {
				done <- err
				return
			}
			// The guest confirms that connect finished before the server closes.
			// Otherwise a reset can be reported by connect instead of the read.
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				conn.Close()
				done <- err
				return
			}
			var ready [1]byte
			if _, err := io.ReadFull(conn, ready[:]); err != nil || ready[0] != 120 {
				conn.Close()
				done <- fmt.Errorf("read-reset marker: got %v, error %v", ready, err)
				return
			}
			// A WASI stream drop can shut down the connection cleanly. Zero
			// linger on the host socket makes the second close send a reset.
			if i == 1 {
				if err := conn.SetLinger(0); err != nil {
					conn.Close()
					done <- err
					return
				}
			}
			if err := conn.Close(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return fmt.Sprintf(`import "std/net";
import "std/time";

function main(): i32 {
    let addr = net.socket_addr(net.ipv4_loopback(), %d);
    let wait = time.duration_seconds(5 as i64);
    for i in 0..2 {
        let Ok(sock) = net.connect(addr) else { print("connect failed"); return 1; };
        match (net.write(sock, [120u8])) {
            Ok(n) => { if (n != 1) { print("connect marker was not sent"); return 5; } },
            Err(_) => { print("connect marker write failed"); return 5; }
        }
        let received = net.read_deadline(sock, 16, wait);
        let closed = net.close(sock);
        match (received) {
            Ok(net.Chunk(bytes)) => {
                if (i != 0 || bytes.len() != 0) { print("reset reported as data or EOF"); return 2; }
            },
            Err(_) => { if (i != 1) { print("clean close reported as error"); return 3; } },
            _ => { print("unexpected read result"); return 4; }
        }
    }
    print("ok");
    return 42;
}
`, listener.Addr().(*net.TCPAddr).Port)
}

func TestSocketCtlWasm(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not on PATH", tool)
		}
	}
	fern := buildFernCLI(t)
	for _, p := range socketProbes {
		t.Run(p.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			program := p.src()
			if p.name == "read_reset" {
				program = wasmReadResetProbe(t)
			}
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			component := filepath.Join(dir, "probe.component.wasm")
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", component, src).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
			}
			out, err := exec.Command("wasmtime", "run", "-S", "inherit-network", component).CombinedOutput()
			if _, exited := err.(*exec.ExitError); err != nil && !exited {
				t.Fatalf("wasmtime run: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "ok" {
				t.Fatalf("wasm: want \"ok\" on stdout; first failing check: %s", got)
			}
		})
	}
}
