package e2e

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestExternImportWithBuiltinUDPViaCLI is the parity gate for the
// world-driven composer's coverage of the UDP socket surface
// (docs/WIT-BRING-YOUR-OWN.md). Sockets had only ever composed through the
// bespoke native registry (component.Compose with req.Udp); routing them
// through ComposeFromWorldAuto is new ground — it must wire the whole UDP
// flow generically: create-udp-socket, udp-socket.start-bind / finish-bind /
// stream, outgoing-datagram-stream.check-send / send / subscribe, plus the
// three resource-drops (udp-socket, outgoing-datagram-stream,
// incoming-datagram-stream). Before `wasi:sockets/udp` was added to the
// `fern` world, an extern program that also called `udp_send()` failed to
// compose with "core imports interface \"wasi:sockets/udp@0.2.0\" not
// declared by the world".
//
// The extern is get-random-bytes (a composite-result import the legacy
// registry doesn't recognise, so its presence forces the whole module
// through the world path). A real UDP listener confirms the datagram the
// world-composed component sends actually lands.
func TestExternImportWithBuiltinUDPViaCLI(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	const want = "world-udp-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

function main(): i32 {
	let b: u8[] = rand_bytes(4 as u64);
	if (b.len() != 4) { return 2; }
	if (udp_send("127.0.0.1", ` + strconv.Itoa(udpPort) + `, "` + want + `") > 0) { return 0; }
	return 1;
}`
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	compPath := filepath.Join(dir, "prog.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, "wasm32-wasi", progPath, compPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host fern -target wasm32-wasi: %v\n%s", err, out)
	}
	if out, err := exec.Command(wasmtime, "run", "-S", "inherit-network", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}

	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	if got := string(buf[:n]); got != want {
		t.Fatalf("datagram = %q; want %q", got, want)
	}
}
