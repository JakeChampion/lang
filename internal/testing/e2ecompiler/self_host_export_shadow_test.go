package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// C uses the public name even when Fern gives the function an internal name.
// A callback alone must also keep its target alive.
func TestSelfHostExportBuiltinShadow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux shared objects require a Linux host")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(src, []byte(`function env(x: i32): i32 { return x + 1; }
@callback("env") function env_entry(): usize;
function get_env(): usize { return env_entry(); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(dir, "host.c")
	if err := os.WriteFile(host, []byte(`#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
int main(int argc, char **argv) {
  void *lib = dlopen(argv[1], RTLD_NOW);
  if (!lib) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*direct)(int) = dlsym(lib, "env");
  uintptr_t (*get_env)(void) = dlsym(lib, "get_env");
  if (!get_env || (argc == 3 && !direct)) { return 101; }
  if (direct && direct(41) != 42) { return 102; }
  int (*callback)(int) = (int (*)(int))get_env();
  if (callback(41) != 42 || callback(-2) != -1) { return 103; }
  return 0;
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc, runner := freestandingTooling(t, cli, strings.Replace(target, "linux", "freestanding", 1))
			bin := filepath.Join(dir, target+"-host")
			if msg, err := exec.Command(gcc, "-o", bin, host, "-ldl").CombinedOutput(); err != nil {
				t.Fatalf("build C host: %v\n%s", err, msg)
			}
			for _, exports := range []string{"env,get_env", "get_env"} {
				t.Run(exports, func(t *testing.T) {
					so := filepath.Join(t.TempDir(), "lib.so")
					if msg, err := cli.compileCmd(target, so, src, "-shared", "-export", exports).CombinedOutput(); err != nil {
						t.Fatalf("compile exports: %v\n%s", err, msg)
					}
					cmd := runner(bin)
					cmd.Args = append(cmd.Args, so)
					if strings.HasPrefix(exports, "env,") {
						cmd.Args = append(cmd.Args, "direct")
					}
					if target == "arm64-linux" && runtime.GOARCH != "arm64" {
						cmd.Env = append(os.Environ(), "QEMU_LD_PREFIX=/usr/aarch64-linux-gnu")
					}
					if target == "x86-64-linux" && runtime.GOARCH != "amd64" {
						cmd.Env = append(os.Environ(), "QEMU_LD_PREFIX=/usr/x86_64-linux-gnu")
					}
					if msg, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("call export and callback: %v\n%s", err, msg)
					}
				})
			}
		})
	}
}
