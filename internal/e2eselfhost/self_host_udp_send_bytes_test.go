package e2eselfhost

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
)

const udpSendBytesProgram = `function main(): i32 {
    let addr: u8[] = UDP_ADDRESS;
    let fd: i32 = udp_bind(addr, 0);
    assert(fd >= 0);
    let data: u8[] = [];
    for i in 0..8193 { data = data.append((i % 256) as u8); }
    let retained: u8[] = data;
    assert(udp_sendto_bytes(fd, addr, UDP_PORT, data) == data.len());
    assert(udp_sendto_bytes(fd, addr, UDP_PORT, retained) == retained.len());
    assert(udp_sendto_bytes(fd, addr, UDP_PORT, [255u8, 0u8, 128u8]) == 3);
    let empty: u8[] = [];
    assert(udp_sendto_bytes(fd, addr, UDP_PORT, empty) == 0);
    assert(udp_connect(fd, addr, UDP_PORT) == 0);
    assert(udp_sendto_bytes(fd, empty, 0, [0u8, 255u8]) == 2);
    assert(tcp_close(fd) == 0);
    UDP_ONESHOT
    assert(data.len() == 8193 && retained.len() == 8193);
    for i in 0..8193 { assert(data[i] == (i % 256) as u8); }
    assert(addr[addr.len() - 1] == 1u8);
    return 0;
}
`

func TestSelfHostUDPSendBytes(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	_, targets, _ := hostTargets()
	if _, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi"})
	}
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		for _, target := range targets {
			backends := []string{""}
			if compiler.name == "bootstrap" && strings.HasSuffix(target.target, "-linux") {
				backends = append(backends, "ssa")
			}
			for _, backend := range backends {
				t.Run(compiler.name+"/"+target.target+"/"+backend, func(t *testing.T) {
					compile := func(src, bin string) *exec.Cmd {
						args := []string{"-target", target.target, "-o", bin}
						if backend != "" {
							args = append(args, "-backend", backend)
						}
						if compiler.name == "primary" && target.target == "wasm32-wasi" {
							args = append(args, "-emit", "core-module")
						}
						args = append(args, src)
						if compiler.name == "primary" {
							args = append(args, stdlib)
						}
						cmd := exec.Command(compiler.path, args...)
						cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("compile: %v\n%s", err, out)
						}
						if target.target == "wasm32-wasi" {
							if compiler.name == "primary" {
								return composeSelfHostWat(t, bin)
							}
							return exec.Command("wasmtime", "run", "-S", "inherit-network", bin)
						}
						return runX86_64Bin(target.runner, bin)
					}
					checkUDPSendBytesFamilies(t, compile, compiler.name == "primary" && target.target != "wasm32-wasi")
				})
			}
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		checkUDPSendBytesFamilies(t, func(src, _ string) *exec.Cmd {
			return exec.Command(bootstrap, "-interp", src)
		}, false)
	})
}

func checkUDPSendBytesFamilies(t *testing.T, compile func(string, string) *exec.Cmd, census bool) {
	t.Helper()
	for _, family := range []struct{ network, host, address string }{
		{"udp4", "127.0.0.1", "[127u8, 0u8, 0u8, 1u8]"},
		{"udp6", "::1", "[0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8]"},
	} {
		t.Run(family.network, func(t *testing.T) {
			peer, err := net.ListenUDP(family.network, &net.UDPAddr{IP: net.ParseIP(family.host)})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			port := strconv.Itoa(peer.LocalAddr().(*net.UDPAddr).Port)
			data := make([]byte, 8193)
			for i := range data {
				data[i] = byte(i)
			}
			want := [][]byte{data, data, {255, 0, 128}, {}, {0, 255}}
			oneshot := ""
			if family.network == "udp4" {
				oneshot = `assert(udp_send_bytes("127.0.0.1", UDP_PORT, data) == data.len());
    assert(udp_send_bytes("127.0.0.1", UDP_PORT, empty) == 0);
    assert(udp_send_bytes("invalid", UDP_PORT, data) < 0);`
				want = append(want, data, []byte{})
			}
			program := strings.NewReplacer("UDP_ONESHOT", oneshot, "UDP_ADDRESS", family.address).Replace(udpSendBytesProgram)
			program = strings.ReplaceAll(program, "UDP_PORT", port)
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
					n, _, err := peer.ReadFromUDP(buf)
					if err != nil {
						received <- err
						return
					}
					if !bytes.Equal(buf[:n], expected) {
						received <- fmt.Errorf("datagram %d: got %d bytes, want %d exact bytes", i, n, len(expected))
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
