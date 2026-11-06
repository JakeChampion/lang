package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// libc's qsort calls the Fern comparator through the address that the
// @callback declaration returns.
const qsortCallbackSrc = `@import("libc.so.6", "qsort")
function c_qsort(base: usize, n: usize, size: usize, cmp: usize): void;

@callback("descending")
function descending_entry(): usize;

function descending(a: usize, b: usize): i32 {
  unsafe {
    return __load_i32(b) - __load_i32(a);
  }
}

pub function sorted(): i32 {
  unsafe {
    let buf: usize = __alloc(16);
    __store_i32(buf, 3);
    __store_i32(buf + 4, 9);
    __store_i32(buf + 8, 1);
    __store_i32(buf + 12, 5);
    c_qsort(buf, 4, 4, descending_entry());
    let v: i32 = __load_i32(buf) * 1000 + __load_i32(buf + 4) * 100 + __load_i32(buf + 8) * 10 + __load_i32(buf + 12);
    __free(buf, 16);
    return v;
  }
}

function main(): i32 {
  return 0;
}
`

const qsortCallbackLoader = `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char** v) {
  void* h = dlopen(v[1], RTLD_NOW); if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*sorted)(void) = (int(*)(void)) dlsym(h, "sorted");
  if (!sorted) return 101;
  if (dlsym(h, "descending")) return 102;
  int got = sorted();
  if (got != 9531) { fprintf(stderr, "sorted = %d\n", got); return 1; }
  return 42;
}
`

func TestCallbackQsort(t *testing.T) {
	type leg struct {
		target, cc, runner string
		env                []string
	}
	var legs []leg
	if gcc, err := exec.LookPath("gcc"); err == nil && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		legs = append(legs, leg{"x86-64-linux", gcc, "", nil})
	}
	if runtime.GOOS == "linux" {
		gcc, qemu := e2eharness.Arm64Tooling(t)
		var env []string
		if qemu != "" {
			env = []string{"QEMU_LD_PREFIX=/usr/aarch64-linux-gnu"}
		}
		legs = append(legs, leg{"arm64-linux", gcc, qemu, env})
	}
	if len(legs) == 0 {
		t.Skip("no C toolchain for a Linux shared object on this host")
	}
	for _, l := range legs {
		t.Run(l.target, func(t *testing.T) {
			if strings.HasPrefix(l.target, "arm64") {
				e2eharness.Arm64CLibc(t, l.cc)
			}
			so, out := buildImportLib(t, l.target, qsortCallbackSrc, "sorted")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			dir := t.TempDir()
			cpath := filepath.Join(dir, "load.c")
			if err := os.WriteFile(cpath, []byte(qsortCallbackLoader), 0o644); err != nil {
				t.Fatal(err)
			}
			ld := filepath.Join(dir, "load")
			if o, err := exec.Command(l.cc, cpath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
				t.Fatalf("%s loader: %v\n%s", l.cc, err, o)
			}
			cmd := exec.Command(ld, so)
			if l.runner != "" {
				cmd = exec.Command(l.runner, ld, so)
			}
			cmd.Env = append(os.Environ(), l.env...)
			o, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Fatalf("qsort did not sort through the Fern comparator: exit %d\n%s", code, o)
			}
		})
	}
}

func TestCallbackRefusals(t *testing.T) {
	const tail = "\nfunction cmp(a: usize, b: usize): i32 { return 0; }\npub function f(): usize { return entry(); }\nfunction main(): i32 { return f() as i32; }\n"
	cases := []struct{ name, target, src, want string }{
		{"executable", "x86-64-linux", "@callback(\"cmp\")\nfunction entry(): usize;" + tail,
			"@callback entry: a callback needs -shared and a native target"},
		{"wasm", "wasm32-wasi", "@callback(\"cmp\")\nfunction entry(): usize;" + tail,
			"@callback entry: a callback needs -shared and a native target"},
		{"signature", "x86-64-linux", "@callback(\"cmp\")\nfunction entry(n: i32): usize;" + tail,
			"@callback entry: the declaration takes no parameters and returns usize"},
		{"result", "x86-64-linux", "@callback(\"cmp\")\nfunction entry(): i32;" + tail,
			"@callback entry: the declaration takes no parameters and returns usize"},
		{"unknown", "x86-64-linux", "@callback(\"nope\")\nfunction entry(): usize;" + tail,
			"@callback entry: the program has no top-level function nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "m.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-target", c.target}
			if c.name != "executable" && c.name != "wasm" {
				args = append(args, "-shared", "-export", "f")
			}
			args = append(args, "-o", filepath.Join(dir, "out"), path, e2eharness.SelfHostStdlibRoot(t))
			cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), c.want) {
				t.Fatalf("want a refusal with %q, got %v:\n%s", c.want, err, out)
			}
		})
	}
}

// A @callback or a C @import that nothing reaches is not refused, so a
// program for the host or for WASI can import a module for Android and use
// its types.
func TestUnreachedNativeDeclarations(t *testing.T) {
	src := "@callback(\"cmp\")\nfunction entry(): usize;\n@import(\"libc.so.6\", \"getpid\")\nfunction c(): i32;\n" +
		"function cmp(a: usize, b: usize): i32 { return 0; }\npub function f(): usize { return entry() + c() as usize; }\n" +
		"function main(): i32 {\n  print(\"ok\");\n  return 0;\n}\n"
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "m.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-o", filepath.Join(dir, "out"), path, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.ChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("an unreached callback and import are refused: %v\n%s", err, out)
			}
			if target != "x86-64-linux" {
				return
			}
			run := exec.Command(filepath.Join(dir, "out"))
			if runner := e2eharness.X86_64Runner(t); len(runner) > 0 {
				run = exec.Command(runner[0], append(runner[1:], filepath.Join(dir, "out"))...)
			}
			out, err := run.CombinedOutput()
			if err != nil || string(out) != "ok\n" {
				t.Fatalf("run: %v, output %q", err, out)
			}
		})
	}
}
