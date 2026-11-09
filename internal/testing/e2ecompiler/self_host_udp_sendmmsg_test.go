package e2ecompiler

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Three messages in one call: a datagram of five bytes, a message of 303
// bytes with a segment size of 100 that the kernel splits into four
// datagrams, marked TOS 2, and an empty datagram; then one message on the
// connected socket with no address, marked TOS 2. The receiver sees seven
// datagrams, in order, each with the TOS byte of its message.
const udpSendmmsgProgram = `function main(): i32 {
    let addr: u8[] = UDP_ADDRESS;
    let fd: Socket = udp_bind(addr, 0);
    assert(fd.fd >= 0);
    let port: i32 = UDP_PORT;
    let to: u8[] = [UDP_FAMILY];
    for b in addr { to = to.append(b); }
    while (to.len() < 17) { to = to.append(0u8); }
    to = to.append((port >> 8) as u8).append((port & 255) as u8);
    let addrs: u8[] = [];
    for m in 0..3 { for b in to { addrs = addrs.append(b); } }
    let data: u8[] = [1u8, 2u8, 3u8, 4u8, 5u8];
    for i in 0..303 { data = data.append((i % 251) as u8); }
    let lens: i32[] = [5, 0, 0, 303, 100, 2, 0, 0, 0];
    assert(udp_sendmmsg(fd, addrs, data, lens) == 3);
    assert(udp_connect(fd, addr, port) == 0);
    let none: u8[] = [];
    assert(udp_sendmmsg(fd, none, [7u8, 8u8], [2, 0, 2]) == 1);
    assert(udp_sendmmsg(fd, none, none, []) == 0);
    assert(udp_sendmmsg(fd, none, [1u8], [2, 0, 0]) == -22);
    assert(tcp_close(fd) == 0);
    assert(data.len() == 308 && addrs.len() == 57);
    return 0;
}
`

func TestSelfHostUDPSendmmsg(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	targets, _ := hostTargets()
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		for _, target := range targets {
			t.Run(compiler.name+"/"+target.target, func(t *testing.T) {
				compile := func(src, bin string) *exec.Cmd {
					args := []string{"-target", target.target, "-o", bin, src}
					if compiler.name == "primary" {
						args = append(args, stdlib)
					}
					cmd := exec.Command(compiler.path, args...)
					cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					return runX86_64Bin(target.runner, bin)
				}
				checkUDPSendmmsgFamilies(t, compile, compiler.name == "primary")
			})
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		checkUDPSendmmsgFamilies(t, func(src, _ string) *exec.Cmd {
			return exec.Command(bootstrap, "-interp", src)
		}, false)
	})
}

func checkUDPSendmmsgFamilies(t *testing.T, compile func(string, string) *exec.Cmd, census bool) {
	t.Helper()
	for _, family := range []struct{ network, host, address, fam string }{
		{"udp4", "127.0.0.1", "[127u8, 0u8, 0u8, 1u8]", "4u8"},
		{"udp6", "::1", "[0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8]", "6u8"},
	} {
		t.Run(family.network, func(t *testing.T) {
			peer, err := net.ListenUDP(family.network, &net.UDPAddr{IP: net.ParseIP(family.host)})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			port := strconv.Itoa(peer.LocalAddr().(*net.UDPAddr).Port)
			train := make([]byte, 303)
			for i := range train {
				train[i] = byte(i % 251)
			}
			want := [][]byte{{1, 2, 3, 4, 5}, train[0:100], train[100:200], train[200:300], train[300:303], {}, {7, 8}}
			wantTOS := []int{0, 2, 2, 2, 2, 0, 2}
			if err := e2eharness.RecvTOS(peer, family.network == "udp6"); err != nil {
				t.Fatal(err)
			}
			program := strings.NewReplacer("UDP_ADDRESS", family.address, "UDP_PORT", port, "UDP_FAMILY", family.fam).Replace(udpSendmmsgProgram)
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "send.fern"), filepath.Join(dir, "send")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			command := compile(src, bin)
			if err := peer.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			received := make(chan error, 1)
			go func() {
				buf := make([]byte, 65536)
				for i, expected := range want {
					n, tos, err := e2eharness.ReadTOS(peer, buf)
					if err != nil {
						received <- fmt.Errorf("datagram %d: %w", i, err)
						return
					}
					if !bytes.Equal(buf[:n], expected) {
						received <- fmt.Errorf("datagram %d: got %d bytes, want %d exact bytes", i, n, len(expected))
						return
					}
					if tos != wantTOS[i] {
						received <- fmt.Errorf("datagram %d: TOS %d, want %d", i, tos, wantTOS[i])
						return
					}
				}
				received <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
			run.Env, run.Dir = command.Env, command.Dir
			out, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("send: %v\n%s", err, out)
			}
			if err := <-received; err != nil {
				t.Fatal(err)
			}
			if census {
				assertBalancedCensus(t, string(out))
			}
		})
	}
}
