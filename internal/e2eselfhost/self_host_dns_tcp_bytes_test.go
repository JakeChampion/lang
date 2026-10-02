package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the production send loop with deterministic short writes and
// failures. Only the three IO calls are replaced by stateful test callbacks.
func dnsTCPBytesFaultSource(t *testing.T) string {
	t.Helper()
	module, err := os.ReadFile("../stdlib/std/dns.fern")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(module), "function send_tcp(")
	if start < 0 {
		t.Fatal("missing DNS TCP sender")
	}
	body := string(module)[start:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatal("missing DNS TCP sender end")
	}
	body = body[:end+3]
	body = strings.Replace(body, "framed: u8[])", "framed: u8[], connect: (net.SocketAddr) => Result[i32, net.NetError], send: (i32, u8[]) => i32, close: (i32) => i32)", 1)
	body = strings.NewReplacer("net.connect(", "connect(", "tcp_send_bytes(", "send(", "tcp_close(", "close(").Replace(body)
	return `import "std/net";
import "std/array";
enum DnsError { NoReply, Socket(net.NetError) }
` + body + `
function probe(mode: i32): void {
    let data: u8[] = [0u8, 255u8, 128u8, 65u8, 0u8, 254u8, 253u8, 10u8];
    if (mode == 6) { data = []; }
    let calls: i32 = 0;
    let progress: i32 = 0;
    let closed: i32 = 0;
    let reset: net.NetError = net.ConnectionReset;
    let interrupted: net.NetError = net.Interrupted;
    let connect = (addr: net.SocketAddr): Result[i32, net.NetError] => {
        if (mode == 5) { return Err(net.ConnectionRefused); }
        return Ok(9);
    };
    let send = (fd: i32, remaining: u8[]): i32 => {
        assert(fd == 9 && closed == 0);
        calls = calls + 1;
        assert(calls <= 4);
        assert(remaining.len() == data.len() - progress);
        for i in 0..remaining.len() { assert(remaining[i] == data[progress + i]); }
        if (mode == 1 && calls == 1) { return 0 - interrupted.errno(); }
        if (mode == 2 || mode == 3 && calls == 2) { return 0 - reset.errno(); }
        if (mode == 4 && calls == 2) { return 0; }
        let n: i32 = remaining.len();
        if (n > 3) { n = 3; }
        progress = progress + n;
        return n;
    };
    let close = (fd: i32): i32 => { assert(fd == 9); closed = closed + 1; return 0; };
    match (send_tcp(net.socket_addr(net.ipv4_loopback(), 53), data, connect, send, close)) {
        Ok(fd) => { assert(mode == 0 || mode == 1 || mode == 6); assert(fd == 9 && progress == data.len()); },
        Err(e) => { match (e) {
            NoReply => { assert(mode == 4); },
            Socket(error) => {
                if (mode == 5) { assert(error.eq(net.ConnectionRefused)); }
                else { assert((mode == 2 || mode == 3) && error.eq(reset)); }
            }
        } }
    }
    if (mode == 0) { assert(calls == 3); }
    if (mode == 1) { assert(calls == 4); }
    if (mode == 2) { assert(calls == 1); }
    if (mode == 3 || mode == 4) { assert(calls == 2 && progress == 3); }
    if (mode == 5 || mode == 6) { assert(calls == 0); }
    if (mode >= 2 && mode <= 4) { assert(closed == 1); }
    else { assert(closed == 0); }
    if (mode != 6) { assert(data.len() == 8 && data[1] == 255u8 && data[7] == 10u8); }
}
function main(): i32 {
    for mode in 0..7 { probe(mode); }
    return 0;
}
`
}

func TestSelfHostDnsTCPBytesFaults(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "dns.fern")
	if err := os.WriteFile(src, []byte(dnsTCPBytesFaultSource(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, targets, _ := hostTargets()
	targets = append(targets, ssaBackendTarget{target: "wasm32-wasi"})
	for _, compiler := range []struct{ name, path string }{{"primary", primary}, {"bootstrap", bootstrap}} {
		for _, target := range targets {
			t.Run(compiler.name+"/"+target.target, func(t *testing.T) {
				bin := filepath.Join(t.TempDir(), "dns")
				args := []string{"-target", target.target, "-o", bin}
				if target.target == "wasm32-wasi" && compiler.name == "primary" {
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
				run := runX86_64Bin(target.runner, bin)
				if target.target == "wasm32-wasi" {
					run = exec.Command("wasmtime", "run", bin)
				}
				out, err := run.CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v\n%s", err, out)
				}
				if compiler.name == "primary" {
					assertBalancedCensus(t, string(out))
				}
			})
		}
		t.Run(compiler.name+"/interpreter", func(t *testing.T) {
			args := []string{"-interp", src}
			if compiler.name == "primary" {
				args = append(args, stdlib)
			}
			if out, err := exec.Command(compiler.path, args...).CombinedOutput(); err != nil {
				t.Fatalf("interpret: %v\n%s", err, out)
			}
		})
	}
}
