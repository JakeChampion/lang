package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the adapter's actual chunk loop with a host substitute that can
// reject writes. Reactor instances do not exit through the census printer,
// so a command program separately verifies cleanup after success and errors.
func TestSelfHostWasiHttpByteWriterCleanup(t *testing.T) {
	source, err := os.ReadFile("../../internal/stdlib/std/wasi_http.fern")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "function write_all(")
	limit := strings.Index(text, "const WRITE_CHUNK:")
	if start < 0 || limit < 0 {
		t.Fatal("HTTP adapter is missing its write loop or chunk limit")
	}
	program := text[limit:limit+strings.Index(text[limit:], ";")+1] + "\n" + text[start:] + `
function stream_write(stream: i32, data: u8[]): Result[i32, i32] {
    assert(data.len() > 0 && data.len() <= WRITE_CHUNK);
    if (stream == 1 || (stream == 2 && data[0] != 0 as u8)) {
        eprint("rejected");
        return Err(1);
    }
    let w: Writer = stdout();
    match (w.write_bytes(data)) { Some(_) => { assert(false); }, None => {} }
    return Ok(0);
}
function main(): i32 {
    let data: u8[] = [];
    for i in 0..8193 { data = data.append((i % 251) as u8); }
    write_all(0, data);
    write_all(1, data);
    write_all(2, data);
    let empty: u8[] = [];
    write_all(1, empty);
    return 0;
}
`
	cli, stdlib := witSelfHostCLI(t)
	_, targets, _ := hostTargets()
	if _, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{"wasmtime", "run"}})
	}
	want := make([]byte, 8193)
	for i := range want {
		want[i] = byte(i % 251)
	}
	want = append(want, want[:4096]...)
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "writer.fern"), filepath.Join(dir, "writer")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-target", target.target, "-o", bin}
			if target.target == "wasm32-wasi" {
				args = append(args, "-emit", "core-module")
			}
			args = append(args, src, stdlib)
			compile := exec.Command(cli, args...)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := runX86_64Bin(target.runner, bin)
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, &stderr)
			}
			if !bytes.Equal(stdout.Bytes(), want) || strings.Count(stderr.String(), "rejected") != 2 {
				t.Fatalf("writes: got %d bytes, want %d; stderr %s", stdout.Len(), len(want), &stderr)
			}
			assertBalancedCensus(t, stderr.String())
		})
	}
}

const wasiHttpByteBodiesSrc = `
import "std/http";
import "std/stream";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    let data: u8[] = req.body_bytes();
    if (req.path == "/stream") {
        return http.stream(200, Stream { data: data, pos: 1 });
    }
    if (req.path == "/chunks") {
        return http.chunks(200, (i: i32): Option[u8[]] => {
            if (i == 0) { return Some(data); }
            if (i == 1) { return Some([0 as u8, 255 as u8]); }
            return None;
        });
    }
    if (req.path == "/text") { return http.ok("aé𐐷z"); }
    return http.bytes(200, data);
}
`

// The primary HTTP adapter must keep byte-domain bodies intact through its
// list<u8> import. Exercise all body producers and both sides of the host's
// 4096-byte write limit against actual wasmtime serve, with native as a second
// compiler rather than the sole expected-output oracle.
func TestSelfHostWasiHttpByteBodies(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	t.Setenv("FERN_STRICT_IR", "1")
	dir := t.TempDir()
	mine := compileWasiHttp(t, dir, wasiHttpByteBodiesSrc, "selfhost.wasm")
	native := buildLangBinForInterp(t)
	theirs := filepath.Join(dir, "native.wasm")
	if out, err := exec.Command(native, "-target", "wasm32-wasi-http", "-o", theirs, filepath.Join(dir, "handler.fern")).CombinedOutput(); err != nil {
		t.Fatalf("native HTTP component: %v\n%s", err, out)
	}
	for _, compiler := range []struct{ name, component string }{{"primary", mine}, {"bootstrap", theirs}} {
		t.Run(compiler.name, func(t *testing.T) {
			for _, n := range []int{0, 1, 4095, 4096, 4097, 8193} {
				payload := make([]byte, n)
				for i := range payload {
					payload[i] = byte(i)
				}
				for _, path := range []string{"/bytes", "/stream", "/chunks"} {
					t.Run(fmt.Sprintf("%s/%d", strings.TrimPrefix(path, "/"), n), func(t *testing.T) {
						want := string(payload)
						if path == "/stream" && n > 0 {
							want = want[1:]
						}
						if path == "/chunks" {
							want += "\x00\xff"
						}
						status, _, got := serveComponent(t, wasmtime, compiler.component, "POST", path, string(payload))
						if status != 200 || got != want {
							t.Fatalf("status %d, body %x; want 200, %x", status, got, want)
						}
					})
				}
			}
			status, _, got := serveComponent(t, wasmtime, compiler.component, "GET", "/text", "")
			if status != 200 || got != "aé𐐷z" {
				t.Fatalf("text body = %d %q; want 200 %q", status, got, "aé𐐷z")
			}
		})
	}
}
