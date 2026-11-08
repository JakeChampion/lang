package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const libcImportSrc = `@import("libc.so.6", "getpid")
function c_getpid(): i32;

function between_imports(): i32 { return 1; }

@import("libc.so.6", "strlen")
function c_strlen(s: usize): usize;

pub function pid(): i32 {
  return c_getpid();
}

pub function len(s: usize): i64 {
  return c_strlen(s) as i64;
}

@import("libc.so.6", "mmap")
function c_mmap(addr: usize, n: usize, prot: i32, flags: i32, fd: i32, off: i64): usize;

@import("libc.so.6", "munmap")
function c_munmap(addr: usize, n: usize): i32;

// Six arguments: an anonymous read-write page.
pub function mapped(): i32 {
  let p: usize = c_mmap(0 as usize, 4096 as usize, 3, 34, 0 - 1, 0i64);
  unsafe {
    __store_i32(p + 100 as usize, 7);
    let v: i32 = __load_i32(p + 100 as usize);
    return v * 10 + c_munmap(p, 4096 as usize);
  }
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
  int (*mapped)(void) = (int(*)(void)) dlsym(h, "mapped");
  if (!pid || !len || !mapped) return 101;
  return pid() == getpid() && len("hello, world") == 12 && mapped() == 70 ? 42 : 1;
}
`

func buildImportLib(t *testing.T, target, src string, exports ...string) (string, []byte) {
	t.Helper()
	return buildImportLibFlags(t, target, src, nil, exports...)
}

func buildImportLibFlags(t *testing.T, target, src string, flags []string, exports ...string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	so := filepath.Join(dir, "libimp.so")
	args := append([]string{}, flags...)
	args = append(args, "-target", target, "-shared", "-export", strings.Join(exports, ","),
		"-o", so, path, e2eharness.SelfHostStdlibRoot(t))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
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
	testSharedCImportsCallLibc(t, false)
}

func TestSharedHardenedCImportsCallLibc(t *testing.T) {
	testSharedCImportsCallLibc(t, true)
}

func testSharedCImportsCallLibc(t *testing.T, harden bool) {
	t.Helper()
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
			if l.machine == elf.EM_AARCH64 {
				e2eharness.Arm64CLibc(t, l.cc)
			}
			var flags []string
			if harden {
				flags = []string{"-harden"}
			}
			so, out := buildImportLibFlags(t, l.target, libcImportSrc, flags, "pid", "len", "mapped")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			raw, err := os.ReadFile(so)
			if err != nil {
				t.Fatal(err)
			}
			sharedImageCheck(t, raw, l.machine)
			if l.machine == elf.EM_AARCH64 {
				f, err := elf.NewFile(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				property := false
				for _, p := range f.Progs {
					property = property || p.Type == elf.PT_GNU_PROPERTY
				}
				if property != harden {
					t.Fatalf("GNU property present=%t, harden=%t", property, harden)
				}
			}
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

// A library with more import slots than ten: the arm64 assembler reads the
// page offset of each slot from its relocation, not from the digits of the
// slot's name (#14048).
func TestSharedCImportsManySlots(t *testing.T) {
	src := ""
	call := "0"
	for i := 0; i < 24; i++ {
		n := strconv.Itoa(i)
		src += "@import(\"libc.so.6\", \"getpid\")\nfunction pid" + n + "(): i32;\n\n"
		call += " + pid" + n + "()"
	}
	src += "pub function pids(): i32 {\n  return " + call + ";\n}\n\nfunction main(): i32 {\n  return 0;\n}\n"
	for _, target := range []string{"arm64-linux", "arm64-android", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			if so, out := buildImportLib(t, target, src, "pids"); so == "" {
				t.Fatalf("build: %s", out)
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
		{"seven params", `@import("libc.so.6", "f") function c(a: i32, b: i32, d: i32, e: i32, g: i32, h: i32, k: i32): i32;`, "x86-64-linux", true, "at most 6 integer parameters and 8 float parameters"},
		{"nine floats", `@import("libm.so.6", "f") function c(a: f64, b: f64, d: f64, e: f64, g: f64, h: f64, k: f64, l: f64, n: f64): f64;`, "x86-64-linux", true, "at most 6 integer parameters and 8 float parameters"},
		{"string param", `@import("libc.so.6", "puts") function c(s: string): i32;`, "x86-64-linux", true, "parameter s is string; a C import takes integer, usize, f32 and f64 parameters"},
		{"string result", `@import("libc.so.6", "getenv") function c(n: usize): string;`, "x86-64-linux", true, "the result is string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An executable refuses only an import that main reaches.
			body := "return 0;"
			if !tc.shared {
				body = "return c();"
			}
			src := tc.decl + "\npub function use_it(): i32 { return 0; }\nfunction main(): i32 { " + body + " }\n"
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

const libmImportSrc = `@import("libm.so.6", "ldexp")
function c_ldexp(x: f64, e: i32): f64;

@import("libm.so.6", "ldexpf")
function c_ldexpf(x: f32, e: i32): f32;

@import("libm.so.6", "fmaf")
function c_fmaf(a: f32, b: f32, c: f32): f32;

@import("libm.so.6", "hypot")
function c_hypot(a: f64, b: f64): f64;

@import("libm.so.6", "lroundf")
function c_lroundf(x: f32): i64;

// Each check is a digit of the result: an int and a float in their own
// registers, an f32 result, three float arguments, and an integer result
// of a float argument.
pub function check(): i32 {
  let n = 0;
  if (c_ldexp(1.5, 3) == 12.0) {
    n += 1;
  }
  if (c_ldexpf(0.75 as f32, 2) == 3.0 as f32) {
    n += 10;
  }
  if (c_fmaf(2.0 as f32, 3.0 as f32, 0.5 as f32) == 6.5 as f32) {
    n += 100;
  }
  if (c_hypot(3.0, 4.0) == 5.0) {
    n += 1000;
  }
  if (c_lroundf(2.6 as f32) == 3i64) {
    n += 10000;
  }
  return n;
}

function main(): i32 {
  return 0;
}
`

const libmImportLoader = `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char** v) {
  void* h = dlopen(v[1], RTLD_NOW); if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  int (*check)(void) = (int(*)(void)) dlsym(h, "check");
  if (!check) return 101;
  int got = check();
  printf("%d\n", got);
  return got == 11111 ? 42 : 1;
}
`

// A weak C import binds a weak symbol, so the library loads, with every
// symbol bound at once, where the symbol is absent. A weak import of a
// symbol that is present calls it as a strong import does.
const weakImportSrc = `@import("libm.so.6", "fern_no_such_symbol", weak)
function c_missing(x: f64): f64;

@import("libm.so.6", "ldexp", weak)
function c_ldexp(x: f64, e: i32): f64;

pub function check(): i32 {
  let x = c_ldexp(1.5, 3);
  if (x < 0.0) {
    return c_missing(x) as i32;
  }
  if (x == 12.0) {
    return 11111;
  }
  return 0;
}

function main(): i32 {
  return 0;
}
`

// A C import with float parameters passes each float in the vector
// register that the ABI gives it, beside the integers in their own
// registers, and gives back an f32, an f64 or an integer result.
func TestSharedCImportsFloatArgs(t *testing.T) {
	runImportLoader(t, libmImportSrc)
}

func TestSharedCImportsWeak(t *testing.T) {
	runImportLoader(t, weakImportSrc)
}

// runImportLoader builds src as a shared object for each Linux target the
// host can run, and loads it with RTLD_NOW. Its check() must return 11111.
func runImportLoader(t *testing.T, src string) {
	t.Helper()
	type leg struct {
		target     string
		cc, runner string
		env        []string
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
			if l.target == "arm64-linux" {
				e2eharness.Arm64CLibc(t, l.cc)
			}
			so, out := buildImportLib(t, l.target, src, "check")
			if so == "" {
				t.Fatalf("build: %s", out)
			}
			dir := filepath.Dir(so)
			loader := filepath.Join(dir, "loader.c")
			if err := os.WriteFile(loader, []byte(libmImportLoader), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "loader")
			if o, err := exec.Command(l.cc, "-o", bin, loader, "-ldl").CombinedOutput(); err != nil {
				t.Fatalf("cc: %v\n%s", err, o)
			}
			var cmd *exec.Cmd
			if l.runner != "" {
				cmd = exec.Command(l.runner, bin, so)
			} else {
				cmd = exec.Command(bin, so)
			}
			cmd.Env = append(os.Environ(), l.env...)
			o, err := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Fatalf("loader exit %d (%v), want 42; each digit of the output is one check:\n%s", code, err, o)
			}
		})
	}
}
