package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const libcImportSrc = `@import("libc.so.6", "getpid")
function c_getpid(): i32;

@import("libc.so.6", "strlen")
function c_strlen(s: usize): usize;

pub function pid(): i32 {
  return c_getpid();
}

pub function len(s: usize): i64 {
  return c_strlen(s) as i64;
}

function main(): i32 {
  return 0;
}
`

const libcImportLoader = `#include <dlfcn.h>
#include <stdio.h>
#include <unistd.h>
int main(int c, char** v) {
  void* h = dlopen(v[1], RTLD_NOW); if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*pid)(void) = (int(*)(void)) dlsym(h, "pid");
  long (*len)(const char*) = (long(*)(const char*)) dlsym(h, "len");
  if (!pid || !len) return 101;
  return pid() == getpid() && len("hello, world") == 12 ? 42 : 1;
}
`

func buildImportLib(t *testing.T, target, src string, exports ...string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	so := filepath.Join(dir, "libimp.so")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-shared", "-export", strings.Join(exports, ","),
		"-o", so, path, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", out
	}
	return so, out
}

// The loader of the host fills each import slot, so a Fern library calls
// getpid and strlen in the host's libc.
func TestSharedCImportsCallLibc(t *testing.T) {
	type leg struct {
		target     string
		machine    elf.Machine
		cc, runner string
		env        []string
	}
	var legs []leg
	if gcc, err := exec.LookPath("gcc"); err == nil && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		legs = append(legs, leg{"x86-64-linux", elf.EM_X86_64, gcc, "", nil})
	}
	if runtime.GOOS == "linux" {
		gcc, qemu := e2eharness.Arm64Tooling(t)
		var env []string
		if qemu != "" {
			env = []string{"QEMU_LD_PREFIX=/usr/aarch64-linux-gnu"}
		}
		legs = append(legs, leg{"arm64-linux", elf.EM_AARCH64, gcc, qemu, env})
	}
	if len(legs) == 0 {
		t.Skip("no C toolchain for a Linux shared object on this host")
	}
	for _, l := range legs {
		t.Run(l.target, func(t *testing.T) {
			so, out := buildImportLib(t, l.target, libcImportSrc, "pid", "len")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			raw, err := os.ReadFile(so)
			if err != nil {
				t.Fatal(err)
			}
			sharedImageCheck(t, raw, l.machine)
			dir := t.TempDir()
			cpath := filepath.Join(dir, "load.c")
			if err := os.WriteFile(cpath, []byte(libcImportLoader), 0o644); err != nil {
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
				t.Fatalf("the C imports did not reach libc: exit %d\n%s", code, o)
			}
		})
	}
}

// An import from an Android NDK library: DT_NEEDED names the library, the
// symbol is undefined, and a GLOB_DAT relocation fills the slot.
func TestSharedCImportsAndroidStructure(t *testing.T) {
	const src = `@import("liblog.so", "__android_log_write")
function log_write(prio: i32, tag: usize, msg: usize): i32;

pub function hello(env: usize): i32 {
  return log_write(4, 0 as usize, 0 as usize);
}

function main(): i32 {
  return 0;
}
`
	for _, tc := range []struct {
		target   string
		machine  elf.Machine
		globDat  elf.R_AARCH64
		globDatX elf.R_X86_64
	}{{"arm64-android", elf.EM_AARCH64, elf.R_AARCH64_GLOB_DAT, 0}, {"x86-64-android", elf.EM_X86_64, 0, elf.R_X86_64_GLOB_DAT}} {
		t.Run(tc.target, func(t *testing.T) {
			so, out := buildImportLib(t, tc.target, src, "hello")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			raw, err := os.ReadFile(so)
			if err != nil {
				t.Fatal(err)
			}
			sharedImageCheck(t, raw, tc.machine)
			f, err := elf.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			libs, err := f.ImportedLibraries()
			if err != nil || len(libs) != 1 || libs[0] != "liblog.so" {
				t.Errorf("DT_NEEDED = %v (%v), want [liblog.so]", libs, err)
			}
			syms, err := f.ImportedSymbols()
			if err != nil || len(syms) != 1 || syms[0].Name != "__android_log_write" {
				t.Errorf("undefined symbols = %v (%v), want __android_log_write", syms, err)
			}
			rela := f.Section(".rela.dyn")
			data, err := rela.Data()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for i := 0; i+24 <= len(data); i += 24 {
				info := f.ByteOrder.Uint64(data[i+8:])
				typ := uint32(info)
				if (tc.machine == elf.EM_AARCH64 && typ == uint32(tc.globDat)) || (tc.machine == elf.EM_X86_64 && typ == uint32(tc.globDatX)) {
					found = info>>32 != 0
				}
			}
			if !found {
				t.Errorf("no GLOB_DAT relocation against a symbol in .rela.dyn")
			}
		})
	}
}

func TestSharedCImportRefusals(t *testing.T) {
	cases := []struct {
		name, decl, target string
		shared             bool
		want               string
	}{
		{"executable", `@import("libc.so.6", "getpid") function c(): i32;`, "x86-64-linux", false, "has no dynamic loader; build a -shared object"},
		{"wasm", `@import("libc.so.6", "getpid") function c(): i32;`, "wasm32-wasi", false, "cannot load the C library libc.so.6"},
		{"five params", `@import("libc.so.6", "f") function c(a: i32, b: i32, d: i32, e: i32, g: i32): i32;`, "x86-64-linux", true, "at most 4 parameters"},
		{"float param", `@import("libm.so.6", "sqrt") function c(x: f64): f64;`, "x86-64-linux", true, "parameter x is f64; a C import takes integer and usize parameters"},
		{"string result", `@import("libc.so.6", "getenv") function c(n: usize): string;`, "x86-64-linux", true, "the result is string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decl + "\npub function use_it(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"
			dir := t.TempDir()
			path := filepath.Join(dir, "m.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"-target", tc.target, "-o", filepath.Join(dir, "out")}
			if tc.shared {
				args = append(args, "-shared", "-export", "use_it")
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), append(args, path, e2eharness.SelfHostStdlibRoot(t))...)
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("want a refusal with %q, got %v:\n%s", tc.want, err, out)
			}
		})
	}
}
