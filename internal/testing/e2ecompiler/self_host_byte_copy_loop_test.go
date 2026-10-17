package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const byteCopyLoopFunctions = `@noinline function bytes(n: i32): u8[] {
  let a = __alloc_u8(n); let i = 0;
  while (i < n) { a = a.with(i, (i + 1) as u8); i = i + 1; }
  return a;
}
@noinline function append(own a: u8[], src: u8[], from: i32, n: i32): u8[] {
  let i = 0;
  while (i < n) { a = a.append(src[from + i]); i = i + 1; }
  return a;
}
@noinline function put(own a: u8[], at: i32, src: u8[], from: i32, n: i32): u8[] {
  let i = 0;
  while (i < n) { a = a.with(at + i, src[from + i]); i = i + 1; }
  return a;
}
@noinline function sum(a: u8[]): i32 {
  let i = 0; let n = 0;
  while (i < a.len()) { n = n + (a[i] as i32); i = i + 1; }
  return n;
}
`

func TestSelfHostByteCopyLoops(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, alias := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/alias=%t", target, alias), func(t *testing.T) {
				input, copies := "let src = bytes(4);", 0
				if alias {
					input, copies = "let src = a;", 1
				}
				program := byteCopyLoopFunctions + fmt.Sprintf(`function main(): i32 {
  let a = bytes(4); %s
  let before = __heap_alloc_count();
  let out = put(a, 1, src, 0, 3);
  if (__heap_alloc_count() - before != %di64) { return 1; }
  if (out[0] != 1u8 || out[1] != 1u8 || out[2] != 2u8 || out[3] != 3u8 || sum(src) != 10) { return 2; }
  let more = bytes(200);
  let before2 = __heap_alloc_count();
  let grown = append(out, more, 0, 200);
  if (__heap_alloc_count() - before2 != 1i64 || grown.len() != 204 || grown[203] != 200u8) { return 3; }
  let before3 = __heap_alloc_count();
  let z = put(grown, 0 - 2147483647, more, 2147483647, 0);
  let negative = append(z, more, 0 - 2147483647, 0 - 1);
  if (__heap_alloc_count() != before3 || negative.len() != 204) { return 4; }
  return 0;
}`, input, copies)
				stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("bulk byte loops: exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
				src := filepath.Join(t.TempDir(), "copy.fern")
				if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				assembly, err := os.ReadFile(cli.emit(t, src, target))
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"__fern_bytes_append_range", "__fern_bytes_write_range"} {
					if !strings.Contains(string(assembly), name) {
						t.Errorf("loop did not reach %s", name)
					}
				}
				if target == "wasm32-wasi" {
					bin := cli.wasmComponent(t, src, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					out, err := exec.Command(e2eharness.Wasmtime(t), "run", bin).CombinedOutput()
					if err != nil {
						t.Fatalf("encoded WASI copy: %v\n%s", err, out)
					}
					assertBalancedCensus(t, string(out))
				}
				stderr, code = cli.exitOf(t, program, target, "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("ordinary bulk copy: exit %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}

func TestSelfHostByteCopyLoopRefusalsAndIndex(t *testing.T) {
	const program = byteCopyLoopFunctions + `
@noinline function forward(own a: u8[]): u8[] {
  let i = 0; while (i < 3) { a = a.with(i + 1, a[i]); i = i + 1; } return a;
}
@noinline function indexed(own a: u8[], src: u8[], n: i32): (u8[], i32) {
  let i = 0; while (i < n) { a = a.append(src[i]); i = i + 1; } return (a, i);
}
@noinline function effect(own a: u8[], src: u8[], n: i32): u8[] {
  let i = 0; while (i < n) { a = a.append(src[i]); eprint("."); i = i + 1; } return a;
}
function main(): i32 {
  let a = forward(bytes(4)); if (sum(a) != 4) { return 1; }
  let src = bytes(4); let x = indexed(a, src, 3);
  if (x.1 != 3 || x.0.len() != 7 || x.0[6] != 3u8) { return 2; }
  let z = indexed(bytes(7), src, 0 - 1);
  if (z.1 != 0 || z.0.len() != 7) { return 3; }
  let out = effect(bytes(7), src, 3); if (out.len() != 10) { return 4; } return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("sequential source or final index: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			if !strings.HasPrefix(stderr, ".\n.\n.\n") {
				t.Fatalf("effectful loop lost writes: %s", stderr)
			}
		})
	}
}

func TestSelfHostByteCopyLengthBound(t *testing.T) {
	const program = `@noinline function cat(own a: u8[], src: u8[]): u8[] {
  let i = 0; while (i < src.len()) { a = a.append(src[i]); i = i + 1; } return a;
}
@noinline function data(n: i32): u8[] {
  let a = __alloc_u8(n); let i = 0;
  while (i < n) { a = a.with(i, (i + 1) as u8); i = i + 1; } return a;
}
function main(): i32 {
  let src = data(5); let a = cat(data(2), src);
  if (a.len() != 7 || a[2] != 1u8 || a[6] != 5u8) { return 1; } return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("length-bounded copy: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			src := filepath.Join(t.TempDir(), "cat.fern")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(assembly), "__fern_bytes_append_range") {
				t.Fatal("length-bounded append kept its byte loop")
			}
			if strings.Contains(string(assembly), "__fn___fern_bytes_write_range:") || strings.Contains(string(assembly), "(func $__fern_bytes_write_range ") {
				t.Fatal("append-only program emits the write kernel")
			}
		})
	}
}

func TestSelfHostByteCopyConstantInputs(t *testing.T) {
	const program = `@noinline function copied(own a: u8[], src: u8[], n: i32): u8[] {
  let i = 0; while (i < n) { a = a.with(i, src[i]); i = i + 1; } return a;
}
function main(): i32 {
  let a: u8[] = [9u8, 8u8, 7u8, 6u8]; let src: u8[] = [1u8, 2u8, 3u8, 4u8];
  let out = copied(a, src, 4); if (out[0] != 1u8 || out[3] != 4u8) { return 1; } return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target)
			if code != 0 {
				t.Fatalf("constant-input copy: exit %d\n%s", code, stderr)
			}
			src := filepath.Join(t.TempDir(), "copy.fern")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(assembly), "__fn___fern_bytes_append_range:") || strings.Contains(string(assembly), "(func $__fern_bytes_append_range ") {
				t.Fatal("write-only program emits the append kernel")
			}
		})
	}
}

func TestSelfHostByteCopyLoopBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for name, call := range map[string]string{
			"source":          "append(a, src, 2, 3)",
			"target":          "put(a, 2, src, 0, 3)",
			"negative-source": "append(a, src, 0 - 1, 1)",
			"overflow-target": "put(a, 2147483647, src, 0, 2)",
		} {
			t.Run(target+"/"+name, func(t *testing.T) {
				program := byteCopyLoopFunctions + "function main(): i32 { let a = bytes(4); let src = bytes(4); let out = " + call + "; return out.len(); }"
				stderr, code := cli.exitOf(t, program, target, "FERN_SANITIZE=1")
				if code == 0 {
					t.Fatal("out-of-bounds copy returned")
				}
				if target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Fatalf("bounds: exit %d, want array abort 134\n%s", code, stderr)
				}
			})
		}
	}
}
