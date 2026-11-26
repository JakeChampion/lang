package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The program keeps 256 one-megabyte arrays live. Under a 64M quota the
// heap reaches the end before the 64th and the runtime stops with its
// cause: a native arena exits 125, and a wasm memory traps at its maximum.
// Without the flag every array fits.
const heapLimitSrc = `function main(): i32 {
  let kept: u8[][] = [];
  let i: i32 = 0;
  while (i < 256) {
    kept = kept.append(__alloc_u8(1048576));
    i = i + 1;
    if (i % 32 == 0) { print(i.to_string()); }
  }
  print("done " + kept.len().to_string());
  return 0;
}
`

func TestSelfHostHeapLimit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(heapLimitSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(t *testing.T, flags ...string) (string, string, int) {
		t.Helper()
		bin := filepath.Join(t.TempDir(), "main")
		args := append(append([]string{}, flags...), "-o", bin, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return bin, stderr.String(), cmd.ProcessState.ExitCode()
	}
	wasmtime := e2eharness.Wasmtime(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			run := func(bin string) (string, string, int) {
				t.Helper()
				var c *exec.Cmd
				switch target {
				case "arm64-linux":
					c = runArm64Bin(qemu, bin)
				case "wasm32-wasi":
					c = exec.Command(wasmtime, "run", bin)
				default:
					c = runX86_64Bin(cli.runner, bin)
				}
				var stdout, stderr bytes.Buffer
				c.Stdout, c.Stderr = &stdout, &stderr
				_ = c.Run()
				return stdout.String(), stderr.String(), c.ProcessState.ExitCode()
			}
			bin, errout, code := compile(t, "-target", target, "-heap-limit", "64M")
			if code != 0 {
				t.Fatalf("compile with -heap-limit 64M: exit %d\n%s", code, errout)
			}
			out, errout, code := run(bin)
			stopped := code == 125
			if target == "wasm32-wasi" {
				stopped = code != 0
			}
			if !stopped || !strings.HasPrefix(errout, "fern: out of memory (heap arena exhausted)\n") || !strings.HasPrefix(out, "32\n") || strings.Contains(out, "64\n") || strings.Contains(out, "done") {
				t.Fatalf("64M quota: exit %d, stdout %q, stderr %q; want a stop after 32 and before 64 arrays with the exhaustion cause", code, out, errout)
			}
			bin, errout, code = compile(t, "-target", target)
			if code != 0 {
				t.Fatalf("compile without the flag: exit %d\n%s", code, errout)
			}
			if out, errout, code := run(bin); code != 0 || !strings.HasSuffix(out, "done 256\n") || errout != "" {
				t.Fatalf("control: exit %d, stdout %q, stderr %q; want every array to fit", code, out, errout)
			}
		})
	}
	t.Run("wasm-static-data", func(t *testing.T) {
		big := filepath.Join(t.TempDir(), "big.fern")
		text := "function main(): i32 {\n  print(\"" + strings.Repeat("a", 1200000) + "\".len().to_string());\n  return 0;\n}\n"
		if err := os.WriteFile(big, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"-target", "wasm32-wasi", "-heap-limit", "1M", "-o", filepath.Join(t.TempDir(), "big"), big, cli.stdlib}
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 2 || !strings.HasPrefix(stderr.String(), "fern: -heap-limit 1048576 is below the ") {
			t.Fatalf("1M quota under 1.2M of data: exit %d, stderr %q; want 2 and the refusal", code, stderr.String())
		}
	})
	t.Run("wasm-manifest", func(t *testing.T) {
		bin, errout, code := compile(t, "-target", "wasm32-wasi", "-heap-limit", "64M")
		if code != 0 {
			t.Fatalf("compile: exit %d\n%s", code, errout)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-manifest", bin)
		cmd.Env = childEnv()
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "heap_limit: 67108864") {
			t.Fatalf("-manifest: %v\n%s; want heap_limit 67108864", err, out)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		for _, tc := range []struct {
			flags []string
			want  string
		}{
			{[]string{"-target", "x86-64-linux", "-heap-limit", "0"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "x86-64-linux", "-heap-limit", "512K"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "x86-64-linux", "-heap-limit", "32G"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "x86-64-linux", "-heap-limit", "99999999999999999999"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "x86-64-linux", "-heap-limit", "17179869185G"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "x86-64-linux", "-heap-limit", "64MB"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "arm64-linux", "-heap-limit", "M"}, "fern: -heap-limit takes a size from 1M to 16G"},
			{[]string{"-target", "wasm32-wasi", "-heap-limit", "8G"}, "fern: -heap-limit takes a size from 1M to 4G for a wasm target"},
			{[]string{"-target", "wasm32-wasi", "-heap-limit", "512K"}, "fern: -heap-limit takes a size from 1M to 4G for a wasm target"},
			{[]string{"-target", "x86-64-freestanding", "-heap-limit", "64M"}, "fern: -heap-limit is only supported for the native and wasm targets (got \"x86-64-freestanding\")"},
		} {
			_, errout, code := compile(t, tc.flags...)
			if code != 2 || !strings.HasPrefix(errout, tc.want) {
				t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.flags, code, errout, tc.want)
			}
		}
		for _, size := range []string{"1M", "1048576", "16G", "17179869184", "1024K"} {
			if _, errout, code := compile(t, "-target", "x86-64-linux", "-emit", "asm", "-heap-limit", size); code != 0 {
				t.Errorf("-heap-limit %s refused: exit %d\n%s", size, code, errout)
			}
		}
		for _, size := range []string{"1M", "4G", "4294967296"} {
			if _, errout, code := compile(t, "-target", "wasm32-wasi", "-heap-limit", size); code != 0 {
				t.Errorf("wasm32-wasi -heap-limit %s refused: exit %d\n%s", size, code, errout)
			}
		}
	})
}
