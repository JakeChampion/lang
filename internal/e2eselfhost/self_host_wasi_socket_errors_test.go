package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/strerror"
)

func TestSelfHostWasiSocketErrors(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "wasm_ir.fern")
	const src = `import "./wasm_ir";
function main(): i32 {
    write(wasm_ir.wasi_socket_errno_func());
    write(wasm_ir.tcp_listen_func());
    return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "socket_errors.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "socket_errors.fern", "socket_errors")
	helper, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime emitter: %v\n%s", err, helper)
	}
	// The host stub writes an Err at the real create-socket return pointer.
	// No socket is opened. Every call goes through the emitted tcp_listen body.
	const prelude = `(module
  (memory 1)
  (global $code (mut i32) (i32.const 0))
  (func $__fern_alloc (param i32) (result i32) (i32.const 16))
  (func $__fern_raw_free (param $p i32) (param $n i32)
    (if (i32.ne (local.get $p) (i32.const 16)) (then unreachable))
    (if (i32.ne (local.get $n) (i32.const 16)) (then unreachable))
    (i32.store offset=4 (local.get $p) (i32.const 255)))
  (func $__fern_network_handle (result i32) (i32.const 1))
  (func $__fern_wasi_create_tcp_socket (param i32) (param $ret i32)
    (i32.store8 (local.get $ret) (i32.const 1))
    (i32.store8 offset=4 (local.get $ret) (global.get $code)))
  (func $__fern_wasi_tcp_start_bind (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32))
  (func $__fern_wasi_tcp_finish_bind (param i32 i32))
  (func $__fern_wasi_tcp_start_listen (param i32 i32))
  (func $__fern_wasi_tcp_finish_listen (param i32 i32))
  (func $__fern_wasi_tcp_socket_drop (param i32) unreachable)
  (func (export "probe") (param $c i32) (result i32)
    (global.set $code (local.get $c))
    (call $__fern_tcp_listen (i32.const 0)))
`
	p := filepath.Join(dir, "socket-errors.wat")
	if err := os.WriteFile(p, []byte(prelude+string(helper)+")"), 0o644); err != nil {
		t.Fatal(err)
	}
	for code := 0; code <= 255; code++ {
		if code >= len(strerror.WasiSocketErrorCodes) && code != 255 {
			continue
		}
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			cmd := exec.Command(wasmtime, "run", "--invoke", "probe", p, strconv.Itoa(code))
			out, err := cmd.Output()
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					t.Fatalf("probe: %v\n%s", err, ee.Stderr)
				}
				t.Fatalf("probe: %v", err)
			}
			got, err := strconv.Atoi(strings.TrimSpace(string(out)))
			want := -strerror.WasiSocketErrno(code)
			if err != nil || got != want || got >= 0 {
				t.Errorf("socket Err(%d) returned %q, want %d", code, out, want)
			}
		})
	}
}
