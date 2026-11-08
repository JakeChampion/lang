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

// The program binds a socket, turns on UDP_GRO and the TOS byte, tells the
// peer its address with one datagram, and then receives in slots of 400
// bytes, three at a time, until the seven-byte closing datagram arrives. It
// prints each message's byte count, segment size, TOS byte, first byte and
// last byte. The peer marks every datagram with TOS 2 and sends five bytes,
// a 350-byte UDP_SEGMENT train of 100-byte datagrams, an empty datagram and
// the closing seven bytes.
const udpRecvmmsgProgram = `function main(): i32 {
    let addr: u8[] = UDP_ADDRESS;
    let fd: Socket = udp_bind(addr, 0);
    assert(fd.fd >= 0);
    let port: i32 = UDP_PORT;
    let gro: i32 = tcp_socket_ctl(fd, 10, 1);
    let tos: i32 = tcp_socket_ctl(fd, UDP_TOS_OP, 1);
    assert(udp_sendto_bytes(fd, addr, port, [104u8, 105u8]) == 2);
    let buf: u8[] = [];
    for i in 0..1200 { buf = buf.append(0u8); }
    let froms: u8[] = [];
    for i in 0..57 { froms = froms.append(0u8); }
    let lens: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 0];
    let closing: boolean = false;
    while (!closing) {
        let n: i32 = udp_recvmmsg(fd, buf, froms, lens);
        assert(n >= 1 && n <= 3);
        for i in 0..n {
            let at: i32 = 19 * i;
            assert(froms[at] == UDP_FAMILY);
            assert((froms[at + 17] as i32 << 8 | froms[at + 18] as i32) == port);
            let got: i32 = lens[3 * i];
            let first: i32 = 0;
            let last: i32 = 0;
            if (got > 0) {
                first = buf[400 * i] as i32;
                last = buf[400 * i + got - 1] as i32;
            }
            print(got.to_string() + " " + lens[3 * i + 1].to_string() + " " + lens[3 * i + 2].to_string() + " " + first.to_string() + " " + last.to_string());
            if (got == 7 && first == 7) {
                closing = true;
            }
        }
    }
    let none: i32[] = [];
    assert(udp_recvmmsg(fd, buf, froms, none) == 0);
    let tiny: u8[] = [];
    assert(udp_recvmmsg(fd, tiny, froms, lens) == -22);
    assert(tcp_close(fd) == 0);
    return 0;
}
`

// What the native bodies report: the train arrives as one message of 350
// bytes with a segment size of 100, and every message carries the TOS byte.
const udpRecvmmsgNative = "5 0 2 1 5\n350 100 2 0 98\n0 0 2 0 0\n7 0 2 7 13\n"

// What the interpreter reports: the pinned stage0 compiler that builds it
// has no socket ops 10 to 12, so the kernel splits the train into its four
// datagrams, and no message carries a segment size or a TOS byte.
const udpRecvmmsgInterp = "5 0 0 1 5\n100 0 0 0 99\n100 0 0 100 199\n100 0 0 200 48\n50 0 0 49 98\n0 0 0 0 0\n7 0 0 7 13\n"

func TestSelfHostUDPRecvmmsg(t *testing.T) {
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
				checkUDPRecvmmsgFamilies(t, compile, udpRecvmmsgNative, compiler.name == "primary")
			})
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		checkUDPRecvmmsgFamilies(t, func(src, _ string) *exec.Cmd {
			return exec.Command(bootstrap, "-interp", src)
		}, udpRecvmmsgInterp, false)
	})
}

func checkUDPRecvmmsgFamilies(t *testing.T, compile func(string, string) *exec.Cmd, want string, census bool) {
	t.Helper()
	for _, family := range []struct {
		network, host, address, fam string
		tosOp                       string
		v6                          bool
	}{
		{"udp4", "127.0.0.1", "[127u8, 0u8, 0u8, 1u8]", "4u8", "11", false},
		{"udp6", "::1", "[0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8]", "6u8", "12", true},
	} {
		t.Run(family.network, func(t *testing.T) {
			peer, err := net.ListenUDP(family.network, &net.UDPAddr{IP: net.ParseIP(family.host)})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			port := strconv.Itoa(peer.LocalAddr().(*net.UDPAddr).Port)
			program := strings.NewReplacer("UDP_ADDRESS", family.address, "UDP_PORT", port, "UDP_FAMILY", family.fam, "UDP_TOS_OP", family.tosOp).Replace(udpRecvmmsgProgram)
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "recv.fern"), filepath.Join(dir, "recv")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			command := compile(src, bin)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
			run.Env, run.Dir = command.Env, command.Dir
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Start(); err != nil {
				t.Fatal(err)
			}
			sent := make(chan error, 1)
			go func() { sent <- feedUDPRecvmmsg(peer, family.v6) }()
			runErr := run.Wait()
			if err := <-sent; err != nil {
				t.Fatalf("peer: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if runErr != nil {
				t.Fatalf("receive: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != want {
				t.Fatalf("messages:\n%s\nwant:\n%s\nstderr:\n%s", got, want, stderr.String())
			}
			if census {
				assertBalancedCensus(t, stderr.String())
			}
		})
	}
}

// feedUDPRecvmmsg waits for the program's hello, then sends the four
// messages the program expects, marked TOS 2.
func feedUDPRecvmmsg(peer *net.UDPConn, v6 bool) error {
	if err := peer.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	hello := make([]byte, 16)
	n, addr, err := peer.ReadFromUDP(hello)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if string(hello[:n]) != "hi" {
		return fmt.Errorf("hello: got %q", hello[:n])
	}
	if err := e2eharness.SetTOS(peer, v6, 2); err != nil {
		return err
	}
	if _, err := peer.WriteToUDP([]byte{1, 2, 3, 4, 5}, addr); err != nil {
		return err
	}
	train := make([]byte, 350)
	for i := range train {
		train[i] = byte(i % 251)
	}
	if err := e2eharness.SendSegmented(peer, addr, train, 100); err != nil {
		return err
	}
	if _, err := peer.WriteToUDP(nil, addr); err != nil {
		return err
	}
	if _, err := peer.WriteToUDP([]byte{7, 8, 9, 10, 11, 12, 13}, addr); err != nil {
		return err
	}
	return nil
}
