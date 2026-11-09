package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/serve marks its HTTP/3 packets ECT(0) on Linux and reports the marks
// it receives in ACK_ECN frames. A client that carries marks both ways
// keeps marking through 20 requests, so the server's counts validated its
// packets, and it counted ECT(0) on the server's packets, so the server
// marked them on the wire.
func TestServeH3EcnX86_64(t *testing.T) {
	fern := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "server.fern"), filepath.Join(dir, "server")
	csrc, cbin := filepath.Join(dir, "client.fern"), filepath.Join(dir, "client")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsH3ServerSource(0, 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(csrc, []byte(e2eharness.ServeH3EcnClientSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, b := range [][2]string{{src, bin}, {csrc, cbin}} {
		if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", b[1], b[0]).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", b[0], err, out)
		}
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, bin))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runX86Bin(qemu, cbin, port, "20").CombinedOutput()
	if err != nil {
		t.Fatalf("client: %v\n%s", err, out)
	}
	if got, want := string(out), "ecn 2 true\n"; got != want {
		t.Fatalf("client reported %q, want %q", got, want)
	}
}
