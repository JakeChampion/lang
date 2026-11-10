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
		{"fifteen params", `@import("libc.so.6", "f") function c(a: i32, b: i32, d: i32, e: i32, g: i32, h: i32, k: i32, l: i32, m: i32, n: i32, o: i32, q: i32, r: i32, t: i32, u: i32): i32;`, "x86-64-linux", true, "at most 14 integer parameters and 8 float parameters"},
		{"nine floats", `@import("libm.so.6", "f") function c(a: f64, b: f64, d: f64, e: f64, g: f64, h: f64, k: f64, l: f64, n: f64): f64;`, "x86-64-linux", true, "at most 14 integer parameters and 8 float parameters"},
		{"string param", `@import("libc.so.6", "puts") function c(s: string): i32;`, "x86-64-linux", true, "parameter s is string; a C import takes integer, usize, f32 and f64 parameters"},
		{"string result", `@import("libc.so.6", "getenv") function c(n: usize): string;`, "x86-64-linux", true, "the result is string"},
		{"struct with no C layout", `struct P { a: i64 } @import("libc.so.6", "f") function c(p: P): i32;`, "x86-64-linux", true, "parameter p is P; a C import takes integer, usize, f32 and f64 parameters, and structs that derive ffi.CLayout"},
		{"struct result with no C layout", `struct P { a: i64 } @import("libc.so.6", "f") function c(): P;`, "arm64-linux", true, "the result is P; a C import returns an integer, a usize, an f32, an f64, a struct that derives ffi.CLayout, or nothing"},
		{"structs above the stack block", `import "std/ffi"; @derive(ffi.CLayout) struct B { a: i64, b: i64, c: i64, d: i64, e: i64 } @import("libc.so.6", "f") function c(p: B, q: B): i32;`, "x86-64-linux", true, "the arguments need more than 64 bytes of stack"},
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
	runImportLoader(t, libmImportSrc, "")
}

func TestSharedCImportsWeak(t *testing.T) {
	runImportLoader(t, weakImportSrc, "")
}

// variadicImportSrc calls snprintf, a variadic C function, through three
// imports of the same symbol: one for each set of argument types.
const variadicImportSrc = `@import("libc.so.6", "snprintf")
function c_format_int(buf: usize, size: usize, format: usize, value: i64): i32;

@import("libc.so.6", "snprintf")
function c_format_float(buf: usize, size: usize, format: usize, value: f64): i32;

@import("libc.so.6", "snprintf")
function c_format_mixed(buf: usize, size: usize, format: usize, a: i32, b: f64, c: usize): i32;

// A NUL-terminated copy of s in C memory.
function cstr(s: string): usize {
  unsafe {
    let p: usize = __alloc(s.len() + 1);
    for i in 0..s.len() {
      __store_u8(p + i as usize, s[i] as i32);
    }
    __store_u8(p + s.len() as usize, 0);
    return p;
  }
}

function text(p: usize): string {
  let out: u8[] = [];
  unsafe {
    let i = 0;
    while (__load_u8(p + i as usize) != 0) {
      out = out.append(__load_u8(p + i as usize) as u8);
      i += 1;
    }
    return string_from_bytes_unchecked(out);
  }
}

pub function check(): i32 {
  let n = 0;
  unsafe {
    let buf: usize = __alloc(64);
    let r1 = c_format_int(buf, 64 as usize, cstr("[%ld]"), -42i64);
    if (text(buf) == "[-42]" && r1 == 5) {
      n += 1;
    }
    let r2 = c_format_float(buf, 64 as usize, cstr("%.2f"), 2.5);
    if (text(buf) == "2.50" && r2 == 4) {
      n += 10;
    }
    let r3 = c_format_mixed(buf, 64 as usize, cstr("%d %.1f %s"), 7, 1.25, cstr("ok"));
    if (text(buf) == "7 1.2 ok") {
      n += 100;
    }
  }
  return n + 11000;
}

function main(): i32 {
  return 0;
}
`

// A variadic C function takes its variadic arguments where it takes fixed
// arguments of their types, so an import with fixed parameters calls it.
func TestSharedCImportsVariadic(t *testing.T) {
	runImportLoader(t, variadicImportSrc, "")
}

// The integer arguments after the last argument register go on the stack
// in their order, beside floats in their own registers.
const manyArgsLib = `#include <stdint.h>
int64_t mix9(int64_t a, int32_t b, int64_t c, int32_t d, int64_t e, int32_t f, int64_t g, int32_t h, int64_t i) {
  return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i;
}
double mixd(int a, int b, int c, int d, int e, int f, int g, int h, int i, int j, double x, float y) {
  return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i + 10*j + x * y;
}
int64_t mix14(int64_t a, int64_t b, int64_t c, int64_t d, int64_t e, int64_t f, int64_t g, int64_t h, int64_t i, int64_t j, int64_t k, int64_t l, int64_t m, int64_t n) {
  return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i + 10*j + 11*k + 12*l + 13*m + 14*n;
}
`

const manyArgsSrc = `@import("libmix.so", "mix9")
function c_mix9(a: i64, b: i32, c: i64, d: i32, e: i64, f: i32, g: i64, h: i32, i: i64): i64;

@import("libmix.so", "mixd")
function c_mixd(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32, x: f64, y: f32): f64;

@import("libmix.so", "mix14")
function c_mix14(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, i: i64, j: i64, k: i64, l: i64, m: i64, n: i64): i64;

// Each check is a digit of the result: nine integers, ten integers with two
// floats, and fourteen integers.
pub function check(): i32 {
  let n = 0;
  if (c_mix9(1i64, 2, 3i64, 4, 5i64, 6, 7i64, 8, -9i64) == 123i64) {
    n += 1;
  }
  if (c_mixd(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 1.5, 3.0 as f32) == 389.5) {
    n += 10;
  }
  if (c_mix14(1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 100i64) == 1491i64) {
    n += 100;
  }
  return n + 11000;
}

function main(): i32 {
  return 0;
}
`

func TestSharedCImportsStackArgs(t *testing.T) {
	runImportLoader(t, manyArgsSrc, manyArgsLib)
}

// runImportLoader builds src as a shared object for each Linux target the
// host can run, and loads it with RTLD_NOW. Its check() must return 11111.
// A non-empty clib is the C source of libmix.so, which src may import.
func runImportLoader(t *testing.T, src, clib string) {
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
			var env []string
			if clib != "" {
				csrc := filepath.Join(dir, "mix.c")
				if err := os.WriteFile(csrc, []byte(clib), 0o644); err != nil {
					t.Fatal(err)
				}
				if o, err := exec.Command(l.cc, "-shared", "-fPIC", "-o", filepath.Join(dir, "libmix.so"), csrc).CombinedOutput(); err != nil {
					t.Fatalf("cc libmix: %v\n%s", err, o)
				}
				env = append(env, "LD_LIBRARY_PATH="+dir)
			}
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
			cmd.Env = append(append(os.Environ(), l.env...), env...)
			o, err := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Fatalf("loader exit %d (%v), want 42; each digit of the output is one check:\n%s", code, err, o)
			}
		})
	}
}

// A struct with fields of each width and with padding, in the layout of C.
const clayoutLib = `#include <stddef.h>
#include <stdint.h>
struct mixed { uint8_t a; int32_t b; uint8_t c; double d; uint32_t e; void* f; float g; };
int64_t mixed_bump(struct mixed* m) {
  int ok = m->a == 7 && m->b == -3 && m->c == 200 && m->d == 2.5 && m->e == 4000000000u && m->f == (void*)99 && m->g == 1.5f;
  m->a += 1; m->b -= 1; m->c += 1; m->d *= 2; m->e += 1; m->f = (char*)m->f + 1; m->g *= 2;
  return ok ? (int64_t)sizeof(struct mixed) : -1;
}
int64_t mixed_e(struct mixed* m) { return m->e; }
int64_t mixed_g_offset(void) { return offsetof(struct mixed, g); }
`

const clayoutSrc = `import "std/ffi";

@derive(ffi.CLayout)
struct Mixed { a: u8, b: i32, c: u8, d: f64, e: u32, f: usize, g: f32 }

@import("libmix.so", "mixed_bump")
function c_mixed_bump(p: usize): i64;

@import("libmix.so", "mixed_e")
function c_mixed_e(p: usize): i64;

@import("libmix.so", "mixed_g_offset")
function c_mixed_g_offset(): i64;

// Each check is a digit of the result: the size and an offset agree with C,
// C reads what Fern writes, Fern reads what C writes, and C reads one field
// that Fern writes.
pub function check(): i32 {
  let n = 0;
  if (Mixed.c_size() as i64 == 48i64 && Mixed.c_offset_g() as i64 == c_mixed_g_offset()) {
    n += 1;
  }
  let p = unsafe { __alloc(Mixed.c_size() as i32) };
  let m = Mixed { a: 7u8, b: -3, c: 200u8, d: 2.5f64, e: 4000000000u32, f: 99 as usize, g: 1.5f32 };
  unsafe {
    m.c_store(p);
  }
  if (c_mixed_bump(p) == 48i64) {
    n += 10;
  }
  let back = unsafe { Mixed.c_load(p) };
  if (back.a == 8u8 && back.b == -4 && back.c == 201u8 && back.d == 5.0f64 && back.e == 4000000001u32 && back.f == 100 as usize && back.g == 3.0f32) {
    n += 100;
  }
  if (unsafe { Mixed.c_load_g(p) } == 3.0f32) {
    n += 1000;
  }
  unsafe {
    Mixed.c_store_e(p, 12345u32);
  }
  if (c_mixed_e(p) == 12345i64) {
    n += 10000;
  }
  return n;
}

function main(): i32 {
  return 0;
}
`

// A struct with @derive(ffi.CLayout) has the offsets and the size of the same
// struct in C, so C and Fern read and write each other's fields.
func TestSharedCImportsCLayout(t *testing.T) {
	runImportLoader(t, clayoutSrc, clayoutLib)
}

// One struct of each class of the two C conventions, by value: two integer
// registers, two vector registers, one of each, a homogeneous struct of
// floats, and a struct in memory. sum_tail takes its last struct on the
// stack, with the integer registers full.
const structValueLib = `#include <stdint.h>
struct pair { int64_t a; int64_t b; };
struct small { int32_t a; uint8_t b; };
struct vec2 { double x; double y; };
struct quad { float a; float b; float c; float d; };
struct mixed { double d; int64_t i; };
struct big { int64_t a; int64_t b; int64_t c; double d; };
int64_t pair_sum(struct pair p) { return p.a * 3 + p.b; }
struct pair pair_swap(struct pair p) { struct pair r = { p.b, p.a }; return r; }
struct small small_bump(struct small s) { struct small r = { s.a - 1, (uint8_t)(s.b + 1) }; return r; }
struct vec2 vec2_scale(struct vec2 v, double k) { struct vec2 r = { v.x * k, v.y * k }; return r; }
struct quad quad_rotate(struct quad q) { struct quad r = { q.d, q.a, q.b, q.c }; return r; }
struct mixed mixed_bump(int32_t n, struct mixed m) { struct mixed r = { m.d + n, m.i * n }; return r; }
struct big big_bump(struct big b, int64_t n) { struct big r = { b.a + n, b.b + n, b.c + n, b.d * 2 }; return r; }
int64_t sum_tail(int64_t a, int64_t b, int64_t c, int64_t d, int64_t e, struct pair p, struct quad q, int64_t f, struct big g) {
  return a + b + c + d + e + p.a * 10 + p.b * 100 + (int64_t)(q.a + q.b + q.c + q.d) * 1000 + f * 10000 + (g.a + g.b + g.c + (int64_t)g.d) * 100000;
}
int64_t late_pair(int64_t a, int64_t b, int64_t c, int64_t d, int64_t e, int64_t f, int64_t g, struct pair p, int64_t h) {
  return a + b + c + d + e + f + g + p.a * 10 + p.b * 100 + h * 1000;
}
double late_vec2(double a, double b, double c, double d, double e, double f, double g, struct vec2 v, double z) {
  return a + b + c + d + e + f + g + v.x * 10 + v.y * 100 + z * 1000;
}
`

const structValueSrc = `import "std/ffi";

@derive(ffi.CLayout)
struct Pair { a: i64, b: i64 }

@derive(ffi.CLayout)
struct Small { a: i32, b: u8 }

@derive(ffi.CLayout)
struct Vec2 { x: f64, y: f64 }

@derive(ffi.CLayout)
struct Quad { a: f32, b: f32, c: f32, d: f32 }

@derive(ffi.CLayout)
struct Mixed { d: f64, i: i64 }

@derive(ffi.CLayout)
struct Big { a: i64, b: i64, c: i64, d: f64 }

@import("libmix.so", "pair_sum")
function c_pair_sum(p: Pair): i64;

@import("libmix.so", "pair_swap")
function c_pair_swap(p: Pair): Pair;

@import("libmix.so", "small_bump")
function c_small_bump(s: Small): Small;

@import("libmix.so", "vec2_scale")
function c_vec2_scale(v: Vec2, k: f64): Vec2;

@import("libmix.so", "quad_rotate")
function c_quad_rotate(q: Quad): Quad;

@import("libmix.so", "mixed_bump")
function c_mixed_bump(n: i32, m: Mixed): Mixed;

@import("libmix.so", "big_bump")
function c_big_bump(b: Big, n: i64): Big;

@import("libmix.so", "sum_tail")
function c_sum_tail(a: i64, b: i64, c: i64, d: i64, e: i64, p: Pair, q: Quad, f: i64, g: Big): i64;

@import("libmix.so", "late_pair")
function c_late_pair(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, p: Pair, h: i64): i64;

@import("libmix.so", "late_vec2")
function c_late_vec2(a: f64, b: f64, c: f64, d: f64, e: f64, f: f64, g: f64, v: Vec2, z: f64): f64;

// A struct that the registers of its kind cannot hold whole goes on the
// stack. The scalar after it takes a register on x86-64 and the stack on
// arm64.
function late(): boolean {
  let ints = c_late_pair(1i64, 1i64, 1i64, 1i64, 1i64, 1i64, 1i64, Pair { a: 2i64, b: 3i64 }, 4i64);
  let floats = c_late_vec2(1.0f64, 1.0f64, 1.0f64, 1.0f64, 1.0f64, 1.0f64, 1.0f64, Vec2 { x: 2.0f64, y: 3.0f64 }, 4.0f64);
  return ints == 4327i64 && floats == 4327.0f64;
}

function registers(): i32 {
  let n = 0;
  let swapped = c_pair_swap(Pair { a: 5i64, b: 9i64 });
  if (c_pair_sum(Pair { a: 4i64, b: 30i64 }) == 42i64 && swapped.a == 9i64 && swapped.b == 5i64) {
    n += 1;
  }
  let small = c_small_bump(Small { a: -7, b: 255u8 });
  if (small.a == -8 && small.b == 0u8) {
    n += 10;
  }
  let v = c_vec2_scale(Vec2 { x: 1.5f64, y: -2.0f64 }, 4.0f64);
  let q = c_quad_rotate(Quad { a: 1.0f32, b: 2.0f32, c: 3.0f32, d: 4.0f32 });
  if (v.x == 6.0f64 && v.y == -8.0f64 && q.a == 4.0f32 && q.b == 1.0f32 && q.c == 2.0f32 && q.d == 3.0f32) {
    n += 100;
  }
  return n;
}

// Each check is a digit of the result: integer registers, a struct smaller
// than its registers, vector registers, one register of each kind with a
// struct in memory, and structs on the stack.
pub function check(): i32 {
  let n = registers();
  let m = c_mixed_bump(3, Mixed { d: 0.5f64, i: 7i64 });
  let b = c_big_bump(Big { a: 1i64, b: 2i64, c: 3i64, d: 1.25f64 }, 10i64);
  if (m.d == 3.5f64 && m.i == 21i64 && b.a == 11i64 && b.b == 12i64 && b.c == 13i64 && b.d == 2.5f64) {
    n += 1000;
  }
  let tail = c_sum_tail(1i64, 1i64, 1i64, 1i64, 1i64, Pair { a: 2i64, b: 3i64 }, Quad { a: 1.0f32, b: 1.0f32, c: 1.0f32, d: 1.0f32 }, 6i64, Big { a: 1i64, b: 2i64, c: 3i64, d: 1.0f64 });
  if (tail == 764325i64 && late()) {
    n += 10000;
  }
  return n;
}

function main(): i32 {
  return 0;
}
`

// A C import takes and returns a struct with @derive(ffi.CLayout) by value,
// in the registers or the memory that the C convention of the target gives
// its class.
func TestSharedCImportsStructByValue(t *testing.T) {
	runImportLoader(t, structValueSrc, structValueLib)
}

const bytesDataLib = `#include <stddef.h>
#include <stdint.h>
int64_t sum_bytes(const uint8_t* p, size_t n) {
  int64_t t = 0;
  for (size_t i = 0; i < n; i++) t += p[i];
  return t;
}
`

const bytesDataSrc = `@import("libmix.so", "sum_bytes")
function c_sum_bytes(p: usize, n: usize): i64;

function sum(b: [u8]): i64 {
  unsafe {
    return c_sum_bytes(__bytes_data(b), b.len() as usize);
  }
}

// Each check is a digit of the result: an owned array, a slice of it, the
// bytes of a string, an empty array, and a large array.
pub function check(): i32 {
  let xs: u8[] = [];
  for i in 0..10 {
    xs = xs.append((i + 1) as u8);
  }
  let big: u8[] = [];
  for i in 0..100000 {
    big = big.append((i % 251) as u8);
  }
  let n = 0;
  if (sum(xs) == 55i64) {
    n += 1;
  }
  if (sum(xs[2:5]) == 12i64) {
    n += 10;
  }
  if (sum("hello, world".as_bytes()) == 1160i64) {
    n += 100;
  }
  let none: u8[] = [];
  if (sum(none) == 0i64) {
    n += 1000;
  }
  if (sum(big) == 12492401i64) {
    n += 10000;
  }
  return n;
}

function main(): i32 {
  return 0;
}
`

// A C function reads the bytes of each form of a byte view where
// __bytes_data points, with no copy.
func TestSharedCImportsBytesData(t *testing.T) {
	runImportLoader(t, bytesDataSrc, bytesDataLib)
}
