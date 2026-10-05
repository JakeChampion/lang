package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func testComponentLongWrites(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "write.fern"), filepath.Join(dir, "write.wasm")
			source := `function main(): i32 {
  let s = ""; let i = 0;
  while (i < 8193) { s = s + "x"; i = i + 1; }
  let w = ` + stream + `();
  match (w.write(s)) { Some(_) => { return 1; }, None => {} }
  match (w.write_some(s)) { Err(_) => { return 2; }, Ok(n) => { if (n != 4096) { return 3; } } }
  return 0;
}`
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, src, stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := exec.Command("wasmtime", "run", bin)
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, stderr.String())
			}
			data, empty := stdout.String(), stderr.String()
			if stream == "stderr" {
				data, empty = stderr.String(), stdout.String()
			}
			// write_some moves one 4096-byte chunk, as native's preview-2 body does.
			if data != strings.Repeat("x", 8193+4096) || empty != "" {
				t.Fatalf("output differs: data=%d other=%d", len(data), len(empty))
			}
		})
	}
}

func TestSelfHostComponentLongWrites(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	testComponentLongWrites(t, cli.bin, cli.runner, cli.stdlib)
}

func TestSelfHostArm64DarwinComponentWrites(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	testComponentLongWrites(t, cli, nil, e2eharness.SelfHostStdlibRoot(t))
	// Emit the actual shim and link it to a deterministic host. This checks
	// failures that a successful wasmtime stdout cannot exercise.
	src := filepath.Join(dir, "shim.fern")
	if err := os.WriteFile(src, []byte(`import "./wasm_ir";
function main(): i32 { write(wasm_ir.component_io_shims([], true, false, false)); return 0; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "shim")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shim compile: %v\n%s", err, out)
	}
	shim, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	testComponentWriteHostOutcomes(t, string(shim))
}

func testComponentWriteHostOutcomes(t *testing.T, shim string) {
	t.Helper()
	for _, tc := range []struct {
		name                                                                 string
		fd, length, vectors, failAt, failureKind, errno, count, calls, drops int
	}{
		{name: "empty", fd: 1, vectors: 1, calls: 1},
		{name: "no vectors", fd: 1},
		{name: "one chunk", fd: 1, length: 4096, vectors: 1, count: 4096, calls: 1},
		{name: "three chunks", fd: 1, length: 8193, vectors: 1, count: 8193, calls: 3},
		{name: "two vectors", fd: 2, length: 8193, vectors: 2, count: 16386, calls: 6},
		{name: "closed", fd: 1, length: 8193, vectors: 1, failAt: 1, failureKind: 1, errno: 29, calls: 1},
		{name: "owned error", fd: 2, length: 8193, vectors: 1, failAt: 1, errno: 29, calls: 1, drops: 1},
		{name: "partial error", fd: 1, length: 8193, vectors: 1, failAt: 2, errno: 29, count: 4096, calls: 2, drops: 1},
		{name: "empty closed", fd: 1, vectors: 1, failAt: 1, failureKind: 1, errno: 29, calls: 1},
		{name: "bad descriptor", fd: 7, length: 8193, vectors: 1, errno: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`
  (func (export "_start")
    (i32.store (i32.const 0) (i32.const 20000)) (i32.store (i32.const 4) (i32.const %d))
    (i32.store (i32.const 8) (i32.const 30000)) (i32.store (i32.const 12) (i32.const %d))
    (global.set $fail_at (i32.const %d)) (global.set $failure_kind (i32.const %d))
    (if (i32.ne (call $__fern_fd_write (i32.const %d) (i32.const 0) (i32.const %d) (i32.const 32)) (i32.const %d)) (then unreachable))
    (if (i32.ne (i32.load (i32.const 32)) (i32.const %d)) (then unreachable))
    (if (i32.ne (global.get $calls) (i32.const %d)) (then unreachable))
    (if (i32.ne (global.get $drops) (i32.const %d)) (then unreachable))
    (if (global.get $live) (then unreachable)))
)`, tc.length, tc.length, tc.failAt, tc.failureKind, tc.fd, tc.vectors, tc.errno, tc.count, tc.calls, tc.drops)
			path := filepath.Join(t.TempDir(), "shim.wat")
			if err := os.WriteFile(path, []byte(componentWriteMockHost+shim+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("wasmtime", "run", path).CombinedOutput(); err != nil {
				t.Fatalf("mock host: %v\n%s", err, out)
			}
		})
	}
}

const componentWriteMockHost = `(module
 (memory 1)
 (global $calls (mut i32) (i32.const 0)) (global $drops (mut i32) (i32.const 0))
 (global $live (mut i32) (i32.const 0)) (global $fail_at (mut i32) (i32.const 0))
 (global $failure_kind (mut i32) (i32.const 0))
 (func $__fern_get_stdout (result i32) (i32.const 11))
 (func $__fern_get_stderr (result i32) (i32.const 12))
 (func $__fern_str_box (param $n i32) (result i32)
   (if (i32.ne (local.get $n) (i32.const 24)) (then unreachable))
   (global.set $live (i32.add (global.get $live) (i32.const 1))) (i32.const 16388))
 (func $__fern_arr_dec (param $p i32) (result i32)
   (if (i32.ne (local.get $p) (i32.const 16388)) (then unreachable))
   (global.set $live (i32.sub (global.get $live) (i32.const 1))) (i32.const 0))
 (func $__fern_io_error_drop (param $h i32)
   (if (i32.ne (local.get $h) (i32.const 77)) (then unreachable))
   (global.set $drops (i32.add (global.get $drops) (i32.const 1))))
 (func $__fern_bwf (param $h i32) (param $ptr i32) (param $len i32) (param $ret i32)
   (if (i32.gt_u (local.get $len) (i32.const 4096)) (then unreachable))
   (if (i32.and (local.get $ret) (i32.const 7)) (then unreachable))
   (global.set $calls (i32.add (global.get $calls) (i32.const 1)))
   (i32.store (local.get $ret) (i32.const 0))
   (if (i32.eq (global.get $calls) (global.get $fail_at)) (then
     (i32.store (local.get $ret) (i32.const 1))
     (i32.store offset=4 (local.get $ret) (global.get $failure_kind))
     (i32.store offset=8 (local.get $ret) (i32.const 77)))))
`
