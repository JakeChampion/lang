package e2ecompiler

import (
	"os"
	"os/exec"
	"testing"
)

// tcp_send_buf sends a builder's bytes from an offset to its end, one send
// per call, and buf_clear empties the builder while keeping its storage: the
// pushes that refill it allocate nothing (exit 3 if they do).
const tcpSendBufProgram = `import "std/bench";
function send_from(fd: i32, b: usize, from: i32): void {
    let at: i32 = from;
    while (at < buf_len(b)) {
        let n: i32 = tcp_send_buf(fd, b, at);
        assert(n > 0 && n <= buf_len(b) - at);
        at = at + n;
    }
}
function fill(b: usize): void {
    for i in 0..8193 { buf_push_byte(b, i % 256); }
}
function main(): i32 {
    let fd: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], TCP_PORT, false);
    assert(fd >= 0);
    let b: usize = buf_new(16);
    fill(b);
    send_from(fd, b, 0);
    send_from(fd, b, 8000);
    assert(tcp_send_buf(fd, b, 8193) == 0);
    assert(tcp_send_buf(fd, b, 9000) == 0);
    buf_clear(b);
    assert(buf_len(b) == 0);
    assert(tcp_send_buf(fd, b, 0) == 0);
    let a0: i64 = bench.alloc_count();
    fill(b);
    let a1: i64 = bench.alloc_count();
    if (a1 != a0) {
        return 3;
    }
    buf_clear(b);
    buf_push_byte(b, 255);
    buf_push_byte(b, 0);
    buf_push_byte(b, 128);
    send_from(fd, b, 0);
    let kept: u8[] = buf_take_bytes(b);
    assert(kept.len() == 3 && kept[0] == 255u8 && kept[2] == 128u8);
    buf_free(b);
    assert(tcp_close(fd) == 0);
    return 0;
}
`

func tcpSendBufWant() []byte {
	all := make([]byte, 8193)
	for i := range all {
		all[i] = byte(i)
	}
	want := append([]byte{}, all...)
	want = append(want, all[8000:]...)
	return append(want, 255, 0, 128)
}

func TestSelfHostTCPSendBuf(t *testing.T) {
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
					compile.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
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
				checkTCPSendBytes(t, compile, tcpSendBufProgram, tcpSendBufWant(), compiler.name == "primary" && target.target != "wasm32-wasi")
			})
		}
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		checkTCPSendBytes(t, func(src, _ string) *exec.Cmd {
			return exec.Command(bootstrap, "-interp", src)
		}, tcpSendBufProgram, tcpSendBufWant(), false)
	})
}
