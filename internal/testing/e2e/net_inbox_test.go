package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// net.Inbox receives several datagrams in one call. With set_udp_gro on, a
// UDP_SEGMENT train of three 1,200-byte datagrams arrives as one message
// whose segments the program walks, and a lone datagram is a message of its
// own. With set_recv_tos on, each message carries the sender's TOS byte.
func TestNetInboxX86_64(t *testing.T) {
	fern := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	port := peer.LocalAddr().(*net.UDPAddr).Port
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "inbox.fern"), filepath.Join(dir, "inbox")
	if err := os.WriteFile(src, []byte(netInboxProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if err := peer.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	fed := make(chan error, 1)
	go func() {
		hello := make([]byte, 16)
		n, addr, err := peer.ReadFromUDP(hello)
		if err != nil {
			fed <- fmt.Errorf("hello: %w", err)
			return
		}
		if string(hello[:n]) != "hi" {
			fed <- fmt.Errorf("hello: got %q", hello[:n])
			return
		}
		if err := e2eharness.SetTOS(peer, false, 2); err != nil {
			fed <- err
			return
		}
		if err := e2eharness.SendSegmented(peer, addr, bytes.Repeat([]byte{0xAB}, 3600), 1200); err != nil {
			fed <- err
			return
		}
		_, err = peer.WriteToUDP(bytes.Repeat([]byte{0xCD}, 100), addr)
		fed <- err
	}()
	out, err := runX86Bin(qemu, bin, strconv.Itoa(port)).CombinedOutput()
	if err := <-fed; err != nil {
		t.Fatalf("peer: %v\n%s", err, out)
	}
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, out)
	}
	if got, w := string(out), "datagrams 1200:171:2:1200 1200:171:2:1200 1200:171:2:1200 100:205:2:0\n"; got != w {
		t.Errorf("inbox reported %q, want %q", got, w)
	}
}

const netInboxProgram = `import "std/net";
import "std/i32";

function main(): i32 {
  let argv: string[] = args();
  let Ok(port) = i32.parse_i32(argv[1]) else { return 2; };
  let Ok(any) = net.socket_addr_parse("127.0.0.1:0") else { return 2; };
  let Ok(peer) = net.socket_addr_parse("127.0.0.1:" + port.to_string()) else { return 2; };
  let Ok(sock) = net.udp_socket(any) else { return 3; };
  let Ok(gro) = net.set_udp_gro(sock, true) else { return 4; };
  let Ok(tos) = net.set_recv_tos(sock, true) else { return 5; };
  let Ok(hello) = net.send_to(sock, [104u8, 105u8], peer) else { return 6; };
  let ib: net.Inbox = net.inbox(4, 65536);
  let out: string = "datagrams";
  let seen: i32 = 0;
  while (seen < 4) {
    let r = ib.receive(sock);
    ib = r.0;
    let Ok(n) = r.1 else { return 7; };
    let i: i32 = 0;
    while (i < n) {
      let at_from: i32 = ib.sender_at(i);
      let from: net.SocketAddr = net.sender(ib.froms[at_from:at_from + net.SENDER_BYTES]);
      if (from.port != port) { return 8; }
      let k: i32 = 0;
      while (k < ib.segments(i)) {
        let (at, len) = ib.segment(i, k);
        out = out + " " + len.to_string() + ":" + (ib.buf[at] as i32).to_string() + ":" + ib.tos_of(i).to_string() + ":" + ib.segment_of(i).to_string();
        seen = seen + 1;
        k = k + 1;
      }
      i = i + 1;
    }
  }
  print(out);
  return 0;
}
`
