package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostComponentPreopens(t *testing.T) {
	e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	testComponentPreopens(t, cli.bin, cli.runner, cli.stdlib)
}

func TestSelfHostArm64DarwinComponentPreopens(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	e2eharness.Wasmtime(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	testComponentPreopens(t, cli, nil, e2eharness.SelfHostStdlibRoot(t))
}

func testComponentPreopens(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	testPackedBytesComponent(t, compiler, runner, stdlib)
	// stat needs a preopen but never opens a descriptor. Compiling it also
	// checks that descriptor-drop is imported for all preopen consumers.
	source := `function main(): i32 {
  let i = 0;
  while (i < 3) {
    match (stat(".")) { Ok(_) => { print("found"); }, Err(_) => { print("missing"); } }
    i = i + 1;
  }
  return 0;
}`
	dir := t.TempDir()
	in, out := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
	if err := os.WriteFile(in, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", out, in, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("directories-%d", count), func(t *testing.T) {
			args := []string{"run"}
			for i := 0; i < count; i++ {
				args = append(args, "--dir", fmt.Sprintf("%s::preopen-%d", dir, i))
			}
			args = append(args, out)
			var stdout, stderr strings.Builder
			run := exec.Command("wasmtime", args...)
			run.Stdout, run.Stderr = &stdout, &stderr
			if err := run.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, stderr.String())
			}
			want := "found\n"
			if count == 0 {
				want = "missing\n"
			}
			if stdout.String() != strings.Repeat(want, 3) {
				t.Fatalf("stdout = %q", stdout.String())
			}
			assertBalancedCensus(t, stderr.String())
			if strings.Contains(stderr.String(), "fern-sanitizer:") {
				t.Fatalf("heap census:\n%s", stderr.String())
			}
		})
	}

	// The real host cannot report which unused descriptors were dropped.
	// Run the emitted helper against a host that checks ownership and caching.
	project := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, project, "wasm_ir.fern")
	src := filepath.Join(project, "preopens.fern")
	if err := os.WriteFile(src, []byte(`import "./wasm_ir";
function main(): i32 { write(wasm_ir.component_fs_shims(["read_file"], false)); return 0; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	shimBin := filepath.Join(project, "preopens")
	target := "x86-64-linux"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		target = "arm64-darwin"
	}
	cmd = runX86_64Bin(runner, compiler, "-target", target, "-o", shimBin, src, stdlib)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shim compile: %v\n%s", err, msg)
	}
	shim, err := runX86_64Bin(runner, shimBin).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("host-ownership-%d", count), func(t *testing.T) {
			want, drops, frees := -1, 0, 1
			if count > 0 {
				want, drops, frees = 40, count-1, count+1
			}
			body := fmt.Sprintf(`
 (func (export "_start")
   (global.set $count (i32.const %d))
   (if (i32.ne (call $__fern_preopen_p2) (i32.const %d)) (then unreachable))
   (if (i32.ne (call $__fern_preopen_p2) (i32.const %d)) (then unreachable))
   (if (i32.ne (global.get $calls) (i32.const 1)) (then unreachable))
   (if (i32.ne (global.get $drops) (i32.const %d)) (then unreachable))
   (if (i32.ne (global.get $frees) (i32.const %d)) (then unreachable))))
`, count, want, want, drops, frees)
			path := filepath.Join(t.TempDir(), "preopens.wat")
			if err := os.WriteFile(path, []byte(componentPreopenMockHost+string(shim)+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if msg, err := exec.Command("wasmtime", "run", path).CombinedOutput(); err != nil {
				t.Fatalf("mock host: %v\n%s", err, msg)
			}
		})
	}
}

const componentPreopenMockHost = `(module
 (memory 1)
 (global $count (mut i32) (i32.const 0)) (global $calls (mut i32) (i32.const 0))
 (global $drops (mut i32) (i32.const 0)) (global $frees (mut i32) (i32.const 0))
 (func $__fern_alloc (param $n i32) (result i32)
   (if (i32.ne (local.get $n) (i32.const 8)) (then unreachable)) (i32.const 16))
 (func $__fern_get_directories (param $ra i32) (local $i i32) (local $entry i32)
   (global.set $calls (i32.add (global.get $calls) (i32.const 1)))
   (i32.store (local.get $ra) (i32.const 64))
   (i32.store offset=4 (local.get $ra) (global.get $count))
   (block $done (loop $next
     (br_if $done (i32.ge_u (local.get $i) (global.get $count)))
     (local.set $entry (i32.add (i32.const 64) (i32.mul (local.get $i) (i32.const 12))))
     (i32.store (local.get $entry) (i32.add (i32.const 40) (local.get $i)))
     (i32.store offset=4 (local.get $entry) (i32.add (i32.const 128) (i32.mul (local.get $i) (i32.const 8))))
     (i32.store offset=8 (local.get $entry) (local.get $i))
     (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $next))))
 (func $__fern_desc_drop (param $h i32)
   (global.set $drops (i32.add (global.get $drops) (i32.const 1)))
   (if (i32.ne (local.get $h) (i32.add (i32.const 40) (global.get $drops))) (then unreachable)))
 (func $__fern_raw_free (param $p i32) (param $n i32) (local $want i32)
   (if (i32.eqz (local.get $n)) (then unreachable))
   (if (i32.eq (local.get $p) (i32.const 16))
     (then (local.set $want (i32.const 8)))
     (else (if (i32.eq (local.get $p) (i32.const 64))
       (then (local.set $want (i32.mul (global.get $count) (i32.const 12))))
       (else (local.set $want (i32.div_u (i32.sub (local.get $p) (i32.const 128)) (i32.const 8)))))))
   (if (i32.ne (local.get $n) (local.get $want)) (then unreachable))
   (if (i32.load offset=256 (local.get $p)) (then unreachable))
   (i32.store offset=256 (local.get $p) (i32.const 1))
   (global.set $frees (i32.add (global.get $frees) (i32.const 1))))
`
