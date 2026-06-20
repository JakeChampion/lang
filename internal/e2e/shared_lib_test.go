// Shared-library (.so) end-to-end tests — slice 2 of the Android app path.
// Compile a Fern program to an x86-64 shared object that EXPORTS a chosen
// function, then dlopen + dlsym + call that export from a gcc-built loader
// on the host. This is the same artifact an Android app would load via
// System.loadLibrary; calling the export proves the dynamic symbol table
// resolves and Fern code runs inside a dlopen'd library (no `_start`, with
// the lazy-mmap heap initialising on first use).
package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

// TestCLISharedX86Dlopen drives the user-facing `-shared` flag end to end:
// `fern -target x86-64 -shared -export answer -o lib.so prog.fern` must
// produce a .so whose `answer` export dlopen+dlsym+calls to 42 (host amd64).
func TestCLISharedX86Dlopen(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	// main does NOT call answer — it survives only because -export keeps it
	// as a tree-shake root (the case a real JVM-only JNI entry needs).
	if err := os.WriteFile(src, []byte("function answer(): i32 { return 42; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	soPath := filepath.Join(dir, "libfern.so")
	if o, err := exec.Command(bin, "-target", "x86-64", "-shared", "-export", "answer", "-o", soPath, src).CombinedOutput(); err != nil {
		t.Fatalf("-shared build failed: %v\n%s", err, o)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char**v){void*h=dlopen(v[1],RTLD_NOW);if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}int(*f)(void)=(int(*)(void))dlsym(h,"answer");if(!f)return 101;return f();}`
	cPath := filepath.Join(dir, "ld.c")
	if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
		t.Fatal(err)
	}
	ld := filepath.Join(dir, "ld")
	if o, err := exec.Command(gcc, cPath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
		t.Fatalf("gcc loader: %v\n%s", err, o)
	}
	cmd := exec.Command(ld, soPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("dlopen+call = %d, want 42 (out=%q)", code, out)
	}
}

// TestCLISharedArm64Structure checks `fern -target arm64-android -shared`
// produces an AArch64 ET_DYN .so with PT_DYNAMIC and the export name in
// .dynstr (the x86 test above exercises dlopen end to end).
func TestCLISharedArm64Structure(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function answer(): i32 { return 42; }\nfunction main(): i32 { return answer(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	soPath := filepath.Join(dir, "libfern.so")
	if o, err := exec.Command(bin, "-target", "arm64-android", "-shared", "-export", "answer", "-o", soPath, src).CombinedOutput(); err != nil {
		t.Fatalf("-shared build failed: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(soPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	if f.Type != elf.ET_DYN || f.Machine != elf.EM_AARCH64 {
		t.Errorf("type/machine = %v/%v, want ET_DYN/AArch64", f.Type, f.Machine)
	}
	dyn := false
	for _, p := range f.Progs {
		if p.Type == elf.PT_DYNAMIC {
			dyn = true
		}
	}
	if !dyn {
		t.Errorf("no PT_DYNAMIC segment")
	}
	if !bytes.Contains(raw, []byte("answer\x00")) {
		t.Errorf(".dynstr does not contain the export name")
	}
}

func TestSharedLibX86ExportDlopen(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH; skipping .so dlopen test")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64; the x86-64 .so can't be dlopen'd natively")
	}
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", `function answer(): i32 { return 42; }
function main(): i32 { return answer(); }`, 42},
		{"arith", `function compute(): i32 { var x = 6; var y = 7; return x * y; }
function main(): i32 { return compute(); }`, 42},
		{"recursion", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1)+fib(n-2); }
function fib10(): i32 { return fib(10); }
function main(): i32 { return fib10(); }`, 55},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			export := c.name // the exported function name varies per case
			switch c.name {
			case "const":
				export = "answer"
			case "arith":
				export = "compute"
			case "recursion":
				export = "fib10"
			}
			asm := compileToX86Asm(t, c.src)
			text, rodata, relocs, exportVAddr, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, []string{export})
			if err != nil {
				t.Fatalf("AssembleProgramShared: %v", err)
			}
			exports := []nativeelf.Export{{Name: export, Value: exportVAddr[export]}}
			so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs), exports, "libfern.so")

			dir := t.TempDir()
			soPath := filepath.Join(dir, "libfern.so")
			if err := os.WriteFile(soPath, so, 0o755); err != nil {
				t.Fatal(err)
			}
			loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int argc, char** argv) {
    void* h = dlopen(argv[1], RTLD_NOW);
    if (!h) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 100; }
    int (*f)(void) = (int(*)(void)) dlsym(h, argv[2]);
    if (!f) { fprintf(stderr, "dlsym: %s\n", dlerror()); return 101; }
    return f();
}`
			cPath := filepath.Join(dir, "loader.c")
			if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
				t.Fatal(err)
			}
			binPath := filepath.Join(dir, "loader")
			if out, err := exec.Command(gcc, cPath, "-ldl", "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("gcc loader: %v\n%s", err, out)
			}
			cmd := exec.Command(binPath, soPath, export)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != c.want {
				t.Fatalf("dlopen+call %s = %d, want %d (out=%q)", export, code, c.want, out)
			}
		})
	}
}

// TestSharedLibX86ExportArgsJNI proves Fern .so exports are C-ABI callable
// *with arguments* (System V AMD64: integer args in rdi/rsi/rdx…), which is
// what makes a Fern function usable as a JNI method directly: a JNI entry is
// a C function (JNIEnv* env, jclass cls, <args>) — env in rdi, cls in rsi,
// the first real arg in rdx. The gcc loader dlsyms the symbol and invokes it
// as `long f(long,long,long)`; SysV passes the args in rdi/rsi/rdx
// regardless of the callee's declared arity, so a Fern function reading its
// Nth param sees the Nth C argument.
func TestSharedLibX86ExportArgsJNI(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	cases := []struct {
		name, fn, src string
		a, b, c, want int
	}{
		// One i32 arg (rdi): triple(14) = 42.
		{"one_arg", "triple", `function triple(x: i32): i32 { return x * 3; }
function main(): i32 { return triple(14); }`, 14, 0, 0, 42},
		// JNI-shaped (env, cls, n): the real arg n is the 3rd param (rdx).
		{"jni_shape", "jni_answer", `function jni_answer(env: i64, cls: i64, n: i32): i32 { return n + 1; }
function main(): i32 { return 0; }`, 0, 0, 41, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Compile with the export as a tree-shake root — jni_answer is
			// never called by main, exactly like a real JVM-only JNI entry.
			asm := compileToX86AsmExports(t, c.src, []string{c.fn})
			text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, []string{c.fn})
			if err != nil {
				t.Fatalf("AssembleProgramShared: %v", err)
			}
			so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
				[]nativeelf.Export{{Name: c.fn, Value: ev[c.fn]}}, "libfern.so")
			dir := t.TempDir()
			soPath := filepath.Join(dir, "libfern.so")
			if err := os.WriteFile(soPath, so, 0o755); err != nil {
				t.Fatal(err)
			}
			loader := `#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
int main(int argc, char** argv) {
    void* h = dlopen(argv[1], RTLD_NOW);
    if (!h) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 100; }
    long (*f)(long,long,long) = (long(*)(long,long,long)) dlsym(h, argv[2]);
    if (!f) { fprintf(stderr, "dlsym: %s\n", dlerror()); return 101; }
    return (int) f(atol(argv[3]), atol(argv[4]), atol(argv[5]));
}`
			cPath := filepath.Join(dir, "loader.c")
			if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
				t.Fatal(err)
			}
			ld := filepath.Join(dir, "loader")
			if out, err := exec.Command(gcc, cPath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
				t.Fatalf("gcc loader: %v\n%s", err, out)
			}
			cmd := exec.Command(ld, soPath, c.fn, decstr(c.a), decstr(c.b), decstr(c.c))
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != c.want {
				t.Fatalf("call %s(%d,%d,%d) = %d, want %d (out=%q)", c.fn, c.a, c.b, c.c, code, c.want, out)
			}
		})
	}
}

func decstr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestSharedLibX86CCallFFI validates the __c_callN FFI primitive: a Fern .so
// export receives a C function pointer and calls it via __c_call0/__c_call1,
// returning its result. The gcc loader passes real callbacks. This is the
// mechanism for calling JNIEnv methods / NDK callbacks from Fern.
func TestSharedLibX86CCallFFI(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	// run0(env,cls,cb) calls cb(); run1(env,cls,cb,x) calls cb(x).
	src := `function run0(env: usize, cls: usize, cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(env: usize, cls: usize, cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function main(): i32 { return 0; }`
	asm := compileToX86AsmExports(t, src, []string{"run0", "run1"})
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, []string{"run0", "run1"})
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "run0", Value: ev["run0"]}, {Name: "run1", Value: ev["run1"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// run0(forty_two)=42 ; run1(dbl,21)=42 — both must come back 42.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long forty_two(void){ return 42; }
static long dbl(long x){ return x*2; }
int main(int c, char**v){
  void*h=dlopen(v[1],RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int(*r0)(long,long,long)=(int(*)(long,long,long))dlsym(h,"run0"); if(!r0)return 101;
  int(*r1)(long,long,long,long)=(int(*)(long,long,long,long))dlsym(h,"run1"); if(!r1)return 102;
  int a=r0(0,0,(long)&forty_two);
  int b=r1(0,0,(long)&dbl,21);
  if(a!=42){fprintf(stderr,"run0=%d\n",a);return 1;}
  if(b!=42){fprintf(stderr,"run1=%d\n",b);return 2;}
  return 42;
}`
	cPath := filepath.Join(dir, "loader.c")
	if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
		t.Fatal(err)
	}
	ld := filepath.Join(dir, "loader")
	if out, err := exec.Command(gcc, cPath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
		t.Fatalf("gcc loader: %v\n%s", err, out)
	}
	cmd := exec.Command(ld, soPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("FFI callbacks via __c_call = exit %d, want 42 (out=%q)", code, out)
	}
}
