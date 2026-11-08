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
)

// net.send_many groups a flight into sendmmsg messages: a run of datagrams of
// one size rides one UDP_SEGMENT message of at most 64 segments and 65,535
// bytes, a shorter datagram closes the run as its last segment, and a lone
// datagram is a plain message. The receiver sees every datagram, whole and
// in order, and the program reports how many messages each flight took. A
// batch sent and filled again sends only its new datagram.
func TestNetSendManyX86_64(t *testing.T) {
	fern := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	port := peer.LocalAddr().(*net.UDPAddr).Port
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "many.fern"), filepath.Join(dir, "many")
	if err := os.WriteFile(src, []byte(netSendManyClient), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	var want [][]byte
	full := bytes.Repeat([]byte{0xAB}, 1200)
	for i := 0; i < 70; i++ {
		want = append(want, full)
	}
	want = append(want, bytes.Repeat([]byte{0xCD}, 300))
	for _, n := range []int{10, 20, 20, 5, 5} {
		want = append(want, bytes.Repeat([]byte{byte(n)}, n))
	}
	want = append(want, []byte{})
	want = append(want, bytes.Repeat([]byte{7}, 7), bytes.Repeat([]byte{9}, 9))
	if err := peer.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	received := make(chan error, 1)
	go func() {
		buf := make([]byte, 65536)
		for i, expected := range want {
			n, _, err := peer.ReadFromUDP(buf)
			if err != nil {
				received <- fmt.Errorf("datagram %d: %w", i, err)
				return
			}
			if !bytes.Equal(buf[:n], expected) {
				received <- fmt.Errorf("datagram %d: got %d bytes, want %d exact bytes", i, n, len(expected))
				return
			}
		}
		received <- nil
	}()
	out, err := runX86Bin(qemu, bin, strconv.Itoa(port)).CombinedOutput()
	if err != nil {
		t.Fatalf("send_many: %v\n%s", err, out)
	}
	if got, w := string(out), "messages 2 3 1 1 1\n"; got != w {
		t.Errorf("send_many reported %q, want %q", got, w)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
}

const netSendManyClient = `import "std/net";
import "std/i32";

function filled(n: i32, b: u8): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(b);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  let argv: string[] = args();
  let Ok(port) = i32.parse_i32(argv[1]) else { return 2; };
  let Ok(any) = net.socket_addr_parse("127.0.0.1:0") else { return 2; };
  let Ok(sock) = net.udp_socket(any) else { return 3; };
  let to: u8[] = [4u8, 127u8, 0u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, (port >> 8) as u8, (port & 255) as u8];
  let train: u8[][] = [];
  let i: i32 = 0;
  while (i < 70) {
    train = train.append(filled(1200, 171u8));
    i = i + 1;
  }
  train = train.append(filled(300, 205u8));
  let Ok(a) = net.send_many(sock, to, train) else { return 4; };
  let Ok(b) = net.send_many(sock, to, [filled(10, 10u8), filled(20, 20u8), filled(20, 20u8), filled(5, 5u8), filled(5, 5u8)]) else { return 5; };
  let empty: u8[] = [];
  let Ok(c) = net.send_many(sock, to, [empty]) else { return 6; };
  let first = net.batch().add(to, [filled(7, 7u8)]).send(sock);
  let Ok(d) = first.1 else { return 7; };
  let second = first.0.add(to, [filled(9, 9u8)]).send(sock);
  let Ok(e) = second.1 else { return 8; };
  print("messages " + a.to_string() + " " + b.to_string() + " " + c.to_string() + " " + d.to_string() + " " + e.to_string());
  return 0;
}
`
