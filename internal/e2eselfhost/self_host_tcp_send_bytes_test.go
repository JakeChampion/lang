package e2eselfhost

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

const tcpSendBytesProgram = `import "std/array";
function send_all(fd: i32, data: u8[]): void {
    let remaining: u8[] = data;
    while (remaining.len() > 0) {
        let n: i32 = tcp_send_bytes(fd, remaining);
        assert(n > 0 && n <= remaining.len());
        remaining = array.drop(remaining, n);
    }
}
function main(): i32 {
    let fd: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], TCP_PORT, false);
    assert(fd >= 0);
    let data: u8[] = [];
    for i in 0..8193 { data = data.append((i % 256) as u8); }
    let retained: u8[] = data;
    send_all(fd, data);
    send_all(fd, retained);
    send_all(fd, [255u8, 0u8, 128u8]);
    let empty: u8[] = [];
    assert(tcp_send_bytes(fd, empty) == 0);
    assert(data.len() == 8193 && retained.len() == 8193);
    for i in 0..8193 { assert(data[i] == (i % 256) as u8); }
    assert(tcp_close(fd) == 0);
    return 0;
}
`

func TestSelfHostTCPSendBytes(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	_, targets, _ := hostTargets()
	if _, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi"})
	}
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		for _, target := range targets {
			t.Run(compiler.name+"/"+target.target, func(t *testing.T) {
				compile := func(src, bin string) *exec.Cmd {
					args := []string{"-target", target.target, "-o", bin}
					if compiler.name == "primary" && target.target == "wasm32-wasi" {
						args = append(args, "-emit", "core-module")
					}
					args = append(args, src)
					if compiler.name == "primary" {
						args = append(args, stdlib)
					}
					compile := exec.Command(compiler.path, args...)
					compile.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if out, err := compile.CombinedOutput(); err != nil {
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
				checkTCPSendBytes(t, compile, compiler.name == "primary" && target.target != "wasm32-wasi")
				if target.target != "wasm32-wasi" {
					for _, shut := range []bool{false, true} {
						program := e2eharness.NativeSocketSendProbe("abc", shut)
						program = strings.ReplaceAll(program, "tcp_send(", "tcp_send_bytes(")
						program = strings.ReplaceAll(program, `let data: string = "abc";`, "let data: u8[] = [255u8, 0u8, 128u8];")
						dir := t.TempDir()
						src, bin := filepath.Join(dir, "errors.fern"), filepath.Join(dir, "errors")
						if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
							t.Fatal(err)
						}
						e2eharness.CheckNativeSocketSend(t, compile(src, bin), "\xff\x00\x80", shut)
					}
				}
			})
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		checkTCPSendBytes(t, func(src, _ string) *exec.Cmd {
			return exec.Command(bootstrap, "-interp", src)
		}, false)
	})
}

func checkTCPSendBytes(t *testing.T, compile func(src, bin string) *exec.Cmd, census bool) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "send.fern"), filepath.Join(dir, "send")
	program := strings.ReplaceAll(tcpSendBytesProgram, "TCP_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	command := compile(src, bin)
	type received struct {
		data []byte
		err  error
	}
	read := make(chan received, 1)
	if err := listener.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() {
		peer, err := listener.AcceptTCP()
		if err != nil {
			read <- received{err: err}
			return
		}
		defer peer.Close()
		if err := peer.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
			read <- received{err: err}
			return
		}
		data, err := io.ReadAll(peer)
		read <- received{data, err}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	run.Env, run.Dir = command.Env, command.Dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("send: %v\n%s", err, out)
	}
	got := <-read
	if got.err != nil {
		t.Fatal(got.err)
	}
	want := make([]byte, 8193)
	for i := range want {
		want[i] = byte(i)
	}
	want = append(append(want, want...), 255, 0, 128)
	if !bytes.Equal(got.data, want) {
		t.Fatalf("socket received %d bytes, want %d exact binary bytes", len(got.data), len(want))
	}
	if census {
		assertBalancedCensus(t, string(out))
	}
}
