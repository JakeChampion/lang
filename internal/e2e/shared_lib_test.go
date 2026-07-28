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
	// run0(env,cls,cb) calls cb(); run1 calls cb(x); run4 calls cb(a,b,c,d)
	// — run4 exercises __c_call4's 4-arg slide (the r8 register).
	src := `function run0(env: usize, cls: usize, cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(env: usize, cls: usize, cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run4(env: usize, cls: usize, cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return 0; }`
	exps := []string{"run0", "run1", "run4"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "run0", Value: ev["run0"]}, {Name: "run1", Value: ev["run1"]}, {Name: "run4", Value: ev["run4"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// run0(forty_two)=42 ; run1(dbl,21)=42 ; run4(sum4,10,11,9,12)=42.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long forty_two(void){ return 42; }
static long dbl(long x){ return x*2; }
static long sum4(long a, long b, long c, long d){ return a+b+c+d; }
int main(int c, char**v){
  void*h=dlopen(v[1],RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int(*r0)(long,long,long)=(int(*)(long,long,long))dlsym(h,"run0"); if(!r0)return 101;
  int(*r1)(long,long,long,long)=(int(*)(long,long,long,long))dlsym(h,"run1"); if(!r1)return 102;
  int(*r4)(long,long,long,long,long,long,long)=(int(*)(long,long,long,long,long,long,long))dlsym(h,"run4"); if(!r4)return 103;
  int a=r0(0,0,(long)&forty_two);
  int b=r1(0,0,(long)&dbl,21);
  int d=r4(0,0,(long)&sum4,10,11,9,12);
  if(a!=42){fprintf(stderr,"run0=%d\n",a);return 1;}
  if(b!=42){fprintf(stderr,"run1=%d\n",b);return 2;}
  if(d!=42){fprintf(stderr,"run4=%d\n",d);return 3;}
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

// TestStdJNIDispatch validates the std/jni outbound bridge: a Fern function
// calls a JNIEnv method (table[index](env, arg)) via jni.call1, dispatched
// through __c_call. A C harness builds a FAKE JNIEnv — a function table whose
// entry at `index` increments its argument — so no real JVM is needed; the
// result proves the env -> table -> method[index](env, arg) indirection and
// the C-ABI call land correctly.
func TestStdJNIDispatch(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function probe1(env: usize, cls: usize, jenv: usize, idx: usize, a0: usize): i32 {
    return jni.call1(jenv, idx as i32, a0) as i32;
}
function main(): i32 { return 0; }`
	asm := compileToX86AsmExports(t, src, []string{"probe1"})
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, []string{"probe1"})
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "probe1", Value: ev["probe1"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake JNIEnv: env -> tptr -> table[]; table[5] = inc(env, x) = x+1.
	// probe1(0, 0, env, 5, 41) -> jni.call1(env, 5, 41) -> table[5](env, 41) = 42.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long inc(void* env, long x){ (void)env; return x + 1; }
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* table[16] = {0};
  table[5] = (void*)inc;
  void* tptr = table;        /* pointer to table[0]            */
  void* env  = &tptr;        /* JNIEnv* = &(table pointer)     */
  int (*probe)(long,long,long,long,long) =
      (int(*)(long,long,long,long,long)) dlsym(h, "probe1");
  if(!probe) return 101;
  return probe(0, 0, (long)env, 5, 41);
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
		t.Fatalf("jni.call1 dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNITypedWrappers validates the typed std/jni wrappers route to the
// correct JNINativeInterface indices. A fake JNIEnv wires the FindClass (6)
// and NewStringUTF (167) slots to an increment fn; the Fern probes call
// jni.find_class / jni.new_string_utf, and each must dispatch to its slot
// (so f(env, 41) -> 42), proving the wrappers carry the right indices.
func TestStdJNITypedWrappers(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function probe_find(env: usize, cls: usize, jenv: usize, a0: usize): i32 {
    return jni.find_class(jenv, a0) as i32;
}
function probe_newstr(env: usize, cls: usize, jenv: usize, a0: usize): i32 {
    return jni.new_string_utf(jenv, a0) as i32;
}
function main(): i32 { return 0; }`
	exps := []string{"probe_find", "probe_newstr"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "probe_find", Value: ev["probe_find"]}, {Name: "probe_newstr", Value: ev["probe_newstr"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// table[6] = FindClass, table[167] = NewStringUTF — both inc(env, x)=x+1.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long inc(void* env, long x){ (void)env; return x + 1; }
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* table[256] = {0};
  table[6]   = (void*)inc;  /* FindClass     */
  table[167] = (void*)inc;  /* NewStringUTF  */
  void* tptr = table; void* env = &tptr;
  int (*pf)(long,long,long,long) = (int(*)(long,long,long,long)) dlsym(h, "probe_find");
  int (*pn)(long,long,long,long) = (int(*)(long,long,long,long)) dlsym(h, "probe_newstr");
  if(!pf||!pn) return 101;
  if(pf(0,0,(long)env,41) != 42) return 1;
  if(pn(0,0,(long)env,41) != 42) return 2;
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
		t.Fatalf("typed wrappers dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIGetMethodId validates the env+3-arg path: jni.get_method_id ->
// call3 -> __c_call4, dispatching to JNINativeInterface slot 33 (GetMethodID)
// with all three real arguments (clazz, name, sig) preserved through the FFI
// shim's arg slide. The fake slot sums its three args, so a result of 42
// proves none was dropped or clobbered.
func TestStdJNIGetMethodId(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function probe_gmid(env: usize, cls: usize, jenv: usize, a0: usize, a1: usize, a2: usize): i32 {
    return jni.get_method_id(jenv, a0, a1, a2) as i32;
}
function main(): i32 { return 0; }`
	exps := []string{"probe_gmid"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "probe_gmid", Value: ev["probe_gmid"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// table[33] = GetMethodID(env, clazz, name, sig) = clazz + name + sig.
	// probe_gmid(0,0,env,10,20,12) -> get_method_id(env,10,20,12) -> 42.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long gmid(void* env, long a, long b, long c){ (void)env; return a + b + c; }
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* table[256] = {0};
  table[33] = (void*)gmid;  /* GetMethodID */
  void* tptr = table; void* env = &tptr;
  int (*pg)(long,long,long,long,long,long) = (int(*)(long,long,long,long,long,long)) dlsym(h, "probe_gmid");
  if(!pg) return 101;
  if(pg(0,0,(long)env,10,20,12) != 42) return 1;
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
		t.Fatalf("get_method_id dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIStaticIds validates the remaining env+3 ID wrappers
// (get_field_id / get_static_method_id / get_static_field_id) carry the
// right JNINativeInterface indices (94 / 113 / 144). Each fake slot returns
// its own marker, so the probe's combined result proves each wrapper landed
// on its slot.
func TestStdJNIStaticIds(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	// Each probe ignores its args and returns the slot's marker; the C side
	// checks field=1, smethod=2, sfield=3 then returns 42.
	src := `import "std/jni";
function probe_field(env: usize, cls: usize, jenv: usize): i32 {
    return jni.get_field_id(jenv, 0 as usize, 0 as usize, 0 as usize) as i32;
}
function probe_smethod(env: usize, cls: usize, jenv: usize): i32 {
    return jni.get_static_method_id(jenv, 0 as usize, 0 as usize, 0 as usize) as i32;
}
function probe_sfield(env: usize, cls: usize, jenv: usize): i32 {
    return jni.get_static_field_id(jenv, 0 as usize, 0 as usize, 0 as usize) as i32;
}
function main(): i32 { return 0; }`
	exps := []string{"probe_field", "probe_smethod", "probe_sfield"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{
			{Name: "probe_field", Value: ev["probe_field"]},
			{Name: "probe_smethod", Value: ev["probe_smethod"]},
			{Name: "probe_sfield", Value: ev["probe_sfield"]},
		}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// table[94]=GetFieldID->1, table[113]=GetStaticMethodID->2, table[144]=GetStaticFieldID->3.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long m1(void* e, long a, long b, long c){ (void)e;(void)a;(void)b;(void)c; return 1; }
static long m2(void* e, long a, long b, long c){ (void)e;(void)a;(void)b;(void)c; return 2; }
static long m3(void* e, long a, long b, long c){ (void)e;(void)a;(void)b;(void)c; return 3; }
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* table[256] = {0};
  table[94]  = (void*)m1;  /* GetFieldID       */
  table[113] = (void*)m2;  /* GetStaticMethodID */
  table[144] = (void*)m3;  /* GetStaticFieldID  */
  void* tptr = table; void* env = &tptr;
  int (*pf)(long,long,long) = (int(*)(long,long,long)) dlsym(h, "probe_field");
  int (*ps)(long,long,long) = (int(*)(long,long,long)) dlsym(h, "probe_smethod");
  int (*pg)(long,long,long) = (int(*)(long,long,long)) dlsym(h, "probe_sfield");
  if(!pf||!ps||!pg) return 101;
  if(pf(0,0,(long)env) != 1) return 1;
  if(ps(0,0,(long)env) != 2) return 2;
  if(pg(0,0,(long)env) != 3) return 3;
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
		t.Fatalf("static-id wrappers dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNICstr validates jni.cstr copies a Fern string into a fresh,
// NUL-terminated C buffer reachable from the dlopen'd .so: the export
// returns jni.cstr("hello"), and the C side checks the bytes round-trip as
// the NUL-terminated string "hello" (exercises the lazy-mmap heap +
// __memcpy + the i32 NUL pad inside a library with no _start).
func TestStdJNICstr(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function probe_cstr(env: usize, cls: usize): usize { return jni.cstr("hello"); }
function main(): i32 { return 0; }`
	exps := []string{"probe_cstr"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "probe_cstr", Value: ev["probe_cstr"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
#include <string.h>
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  char* (*p)(long,long) = (char*(*)(long,long)) dlsym(h, "probe_cstr");
  if(!p) return 101;
  char* s = p(0,0);
  if(!s) return 102;
  if(strcmp(s, "hello") != 0){ fprintf(stderr, "got %s\n", s); return 1; }
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
		t.Fatalf("cstr round-trip = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIExceptionAndRefWrappers validates the exception-handling,
// reference-management, and class-relationship wrappers each dispatch to
// their JNINativeInterface slot. A fake JNIEnv wires each slot to a fn that
// returns the slot index as a marker; every probe must come back with its
// own index, proving the wrapper carries the right number.
func TestStdJNIExceptionAndRefWrappers(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function p_throw(env: usize, cls: usize, j: usize): i32 { return jni.throw(j, 0 as usize); }
function p_thrownew(env: usize, cls: usize, j: usize): i32 { return jni.throw_new(j, 0 as usize, 0 as usize); }
function p_excocc(env: usize, cls: usize, j: usize): i32 { return jni.exception_occurred(j) as i32; }
function p_excdesc(env: usize, cls: usize, j: usize): i32 { return jni.exception_describe(j) as i32; }
function p_excclr(env: usize, cls: usize, j: usize): i32 { return jni.exception_clear(j) as i32; }
function p_newglobal(env: usize, cls: usize, j: usize): i32 { return jni.new_global_ref(j, 0 as usize) as i32; }
function p_delglobal(env: usize, cls: usize, j: usize): i32 { return jni.delete_global_ref(j, 0 as usize) as i32; }
function p_dellocal(env: usize, cls: usize, j: usize): i32 { return jni.delete_local_ref(j, 0 as usize) as i32; }
function p_newlocal(env: usize, cls: usize, j: usize): i32 { return jni.new_local_ref(j, 0 as usize) as i32; }
function p_super(env: usize, cls: usize, j: usize): i32 { return jni.get_superclass(j, 0 as usize) as i32; }
function p_assign(env: usize, cls: usize, j: usize): i32 { return jni.is_assignable_from(j, 0 as usize, 0 as usize); }
function p_same(env: usize, cls: usize, j: usize): i32 { return jni.is_same_object(j, 0 as usize, 0 as usize); }
function p_alloc(env: usize, cls: usize, j: usize): i32 { return jni.alloc_object(j, 0 as usize) as i32; }
function p_instof(env: usize, cls: usize, j: usize): i32 { return jni.is_instance_of(j, 0 as usize, 0 as usize); }
function main(): i32 { return 0; }`
	// probe name -> expected JNINativeInterface slot index.
	probes := []struct {
		fn   string
		slot int
	}{
		{"p_throw", 13}, {"p_thrownew", 14}, {"p_excocc", 15}, {"p_excdesc", 16},
		{"p_excclr", 17}, {"p_newglobal", 21}, {"p_delglobal", 22}, {"p_dellocal", 23},
		{"p_newlocal", 25}, {"p_super", 10}, {"p_assign", 11}, {"p_same", 24},
		{"p_alloc", 27}, {"p_instof", 32},
	}
	exps := make([]string, len(probes))
	for i, p := range probes {
		exps[i] = p.fn
	}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	elfExports := make([]nativeelf.Export, len(probes))
	for i, p := range probes {
		elfExports[i] = nativeelf.Export{Name: p.fn, Value: ev[p.fn]}
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs), elfExports, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// Give each slot a trampoline that returns its own index, then check
	// every probe echoes its slot back — proving each wrapper's index.
	var fns, slotInit, checks string
	for _, p := range probes {
		idx := decstr(p.slot)
		fns += "static long f" + idx + "(void){ return " + idx + "; }\n"
		slotInit += "  table[" + idx + "] = (void*)f" + idx + ";\n"
		checks += "  if(((long(*)(long,long,long))dlsym(h,\"" + p.fn + "\"))(0,0,(long)env) != " + idx + ") return " + idx + ";\n"
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
` + fns + `int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* table[256] = {0};
` + slotInit + `  void* tptr = table; void* env = &tptr;
` + checks + `  return 42;
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
		t.Fatalf("exception/ref wrapper dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIFieldAccessors validates the instance/static field accessors and
// get_array_length: correct slot dispatch, a 64-bit get_long_field value
// (proving the full word survives, not just 32 bits), and a set_int_field
// write that lands in the fake field. A backing `long fld` models the
// instance field so the get/set pair round-trips through the .so.
func TestStdJNIFieldAccessors(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function g_int(env: usize, cls: usize, j: usize): i32 { return jni.get_int_field(j, 0 as usize, 0 as usize); }
function g_long(env: usize, cls: usize, j: usize): usize { return jni.get_long_field(j, 0 as usize, 0 as usize); }
function g_obj(env: usize, cls: usize, j: usize): usize { return jni.get_object_field(j, 0 as usize, 0 as usize); }
function s_int(env: usize, cls: usize, j: usize, v: usize): usize { return jni.set_int_field(j, 0 as usize, 0 as usize, v); }
function g_sint(env: usize, cls: usize, j: usize): i32 { return jni.get_static_int_field(j, 0 as usize, 0 as usize); }
function arrlen(env: usize, cls: usize, j: usize, a: usize): i32 { return jni.get_array_length(j, a); }
function main(): i32 { return 0; }`
	exps := []string{"g_int", "g_long", "g_obj", "s_int", "g_sint", "arrlen"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	elfExports := make([]nativeelf.Export, len(exps))
	for i, n := range exps {
		elfExports[i] = nativeelf.Export{Name: n, Value: ev[n]}
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs), elfExports, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake JNIEnv slots: 100 GetIntField, 101 GetLongField (returns a value
	// >32 bits), 95 GetObjectField, 109 SetIntField (writes fld), 150
	// GetStaticIntField, 171 GetArrayLength.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long fld;
static long g100(void*e,long o,long f){(void)e;(void)o;(void)f;return fld;}
static long g101(void*e,long o,long f){(void)e;(void)o;(void)f;return 0x1ffffffffL;}
static long g95(void*e,long o,long f){(void)e;(void)o;(void)f;return 0xABC;}
static long s109(void*e,long o,long f,long v){(void)e;(void)o;(void)f;fld=v;return 0;}
static long g150(void*e,long c,long f){(void)e;(void)c;(void)f;return 555;}
static long g171(void*e,long a){(void)e;(void)a;return 7;}
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* t[256]={0};
  t[100]=(void*)g100; t[101]=(void*)g101; t[95]=(void*)g95;
  t[109]=(void*)s109; t[150]=(void*)g150; t[171]=(void*)g171;
  void* tp=t; void* env=&tp; long L=(long)env;
  fld=42;
  if(((int(*)(long,long,long))dlsym(h,"g_int"))(0,0,L)!=42) return 1;
  if(((unsigned long(*)(long,long,long))dlsym(h,"g_long"))(0,0,L)!=0x1ffffffffUL) return 2;
  if(((unsigned long(*)(long,long,long))dlsym(h,"g_obj"))(0,0,L)!=0xABC) return 3;
  ((unsigned long(*)(long,long,long,long))dlsym(h,"s_int"))(0,0,L,99);
  if(fld!=99) return 4;
  if(((int(*)(long,long,long))dlsym(h,"g_sint"))(0,0,L)!=555) return 5;
  if(((int(*)(long,long,long,long))dlsym(h,"arrlen"))(0,0,L,0)!=7) return 6;
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
		t.Fatalf("field accessor dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestAsBytesInSharedLib guards the PIE/.so fix for s.as_bytes(): a string
// literal's bytes live in .rodata, which a dlopen'd shared object maps at a
// high (>32-bit) base. Slice headers store the data pointer in 32 bits, so
// without the high-pointer copy guard in __method_string_as_bytes the view
// would alias a truncated, bogus address and read zeroes. Here the export
// returns the as_bytes data pointer for "hello"; the C side must read back
// the real bytes. (Regression guard for the bug found while adding cstr.)
func TestAsBytesInSharedLib(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `function ab_data(env: usize, cls: usize): usize {
    var s: string = "hello";
    var b: [u8] = s.as_bytes();
    return b as usize;
}
function ab_idx(env: usize, cls: usize, i: usize): i32 {
    var s: string = "the quick brown fox";
    var b: [u8] = s.as_bytes();
    return (b[i as i32] as i32);
}
function main(): i32 { return 0; }`
	exps := []string{"ab_data", "ab_idx"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "ab_data", Value: ev["ab_data"]}, {Name: "ab_idx", Value: ev["ab_idx"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
#include <string.h>
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  unsigned char* (*pd)(long,long) = (unsigned char*(*)(long,long)) dlsym(h, "ab_data");
  int (*pi)(long,long,long) = (int(*)(long,long,long)) dlsym(h, "ab_idx");
  if(!pd||!pi) return 101;
  unsigned char* d = pd(0,0);
  if(memcmp(d, "hello", 5) != 0){ fprintf(stderr, "as_bytes data = %.5s\n", d); return 1; }
  /* "the quick brown fox"[16] == 'f' (index past the 32-bit truncation) */
  if(pi(0,0,16) != 'f'){ fprintf(stderr, "idx16 = %d\n", pi(0,0,16)); return 2; }
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
		t.Fatalf("as_bytes in .so = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIFloatFieldAccessors validates the FP-returning field getters:
// get_float_field / get_double_field / get_static_double_field route through
// the __c_call*_f32 / _f64 shims so the jfloat/jdouble result comes back in
// the FP register (xmm0). Fake JNIEnv slots return known FP constants; the C
// side reads them as float/double and the probe echoes them back as a scaled
// int (×4) so a single integer exit code can assert all three.
func TestStdJNIFloatFieldAccessors(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	// Each probe scales its FP result by 4 and truncates to i32, so exact
	// quarter-valued constants round-trip losslessly through the exit code.
	src := `import "std/jni";
function p_float(env: usize, cls: usize, j: usize): i32 { return (jni.get_float_field(j, 0 as usize, 0 as usize) * (4.0 as f32)) as i32; }
function p_double(env: usize, cls: usize, j: usize): i32 { return (jni.get_double_field(j, 0 as usize, 0 as usize) * 4.0) as i32; }
function p_sdouble(env: usize, cls: usize, j: usize): i32 { return (jni.get_static_double_field(j, 0 as usize, 0 as usize) * 4.0) as i32; }
function main(): i32 { return 0; }`
	exps := []string{"p_float", "p_double", "p_sdouble"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	elfExports := make([]nativeelf.Export, len(exps))
	for i, n := range exps {
		elfExports[i] = nativeelf.Export{Name: n, Value: ev[n]}
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs), elfExports, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// table[102]=GetFloatField->2.5, [103]=GetDoubleField->6.25,
	// [153]=GetStaticDoubleField->9.75. Scaled ×4: 10, 25, 39.
	loader := `#include <dlfcn.h>
#include <stdio.h>
static float  f102(void*e,long o,long f){(void)e;(void)o;(void)f;return 2.5f;}
static double d103(void*e,long o,long f){(void)e;(void)o;(void)f;return 6.25;}
static double d153(void*e,long c,long f){(void)e;(void)c;(void)f;return 9.75;}
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void* t[256]={0}; t[102]=(void*)f102; t[103]=(void*)d103; t[153]=(void*)d153;
  void* tp=t; void* env=&tp; long L=(long)env;
  if(((int(*)(long,long,long))dlsym(h,"p_float"))(0,0,L)   != 10) return 1;
  if(((int(*)(long,long,long))dlsym(h,"p_double"))(0,0,L)  != 25) return 2;
  if(((int(*)(long,long,long))dlsym(h,"p_sdouble"))(0,0,L) != 39) return 3;
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
		t.Fatalf("FP field accessor dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdlibInSharedLib proves real stdlib string helpers work inside a
// dlopen'd .so. These all flow through s.as_bytes() over string-literal
// .rodata, which is mapped at a high (>32-bit) base in a PIE shared object —
// the exact case the slice-header fix addresses. Before that fix every one
// of these returned garbage in a .so; this guards the whole class (case
// folding, base64, hex) against regression, and documents that the .so /
// Android target is stdlib-usable, not just JNI-glue-usable.
func TestStdlibInSharedLib(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	// `check` returns a bitmask; each bit is one stdlib round-trip that must
	// hold. 63 == all six pass.
	src := `import "std/string";
import "std/base64";
import "std/hex";
function check(env: usize, cls: usize): i32 {
    var ok: i32 = 0;
    if (("HELLO").to_lower() == "hello") { ok = ok + 1; }
    if (("hello").to_upper() == "HELLO") { ok = ok + 2; }
    if (base64.base64_encode("Man") == "TWFu") { ok = ok + 4; }
    if (base64.base64_decode("TWFu") == "Man") { ok = ok + 8; }
    if (hex.hex_encode("AB") == "4142") { ok = ok + 16; }
    if (string_from_bytes_unchecked(hex.hex_decode("4142")) == "AB") { ok = ok + 32; }
    return ok;
}
function main(): i32 { return 0; }`
	exps := []string{"check"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "check", Value: ev["check"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int (*check)(long,long) = (int(*)(long,long)) dlsym(h, "check");
  if(!check) return 101;
  int r = check(0,0);
  if(r != 63){ fprintf(stderr, "stdlib bitmask = %d, want 63\n", r); return 1; }
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
		t.Fatalf("stdlib in .so = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNICallMethodA validates invoking Java methods through the
// fixed-arity jvalue-array Call<Type>MethodA family + the jvalues packer:
// jni.call_int_method_a (slot 51) sums the packed jvalue args, the FP-return
// jni.call_double_method_a (slot 60, via call3_f64) round-trips a jdouble,
// and jni.call_void_method_a (slot 63) delivers its arg. A fake JNIEnv reads
// the jvalue array (8-byte slots) the packer built.
func TestStdJNICallMethodA(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function p_int(env: usize, cls: usize, j: usize, obj: usize, m: usize): i32 {
    return jni.call_int_method_a(j, obj, m, jni.jvalues([10 as usize, 20 as usize, 12 as usize]));
}
function p_dbl(env: usize, cls: usize, j: usize, obj: usize, m: usize): i32 {
    return (jni.call_double_method_a(j, obj, m, jni.jvalues([3 as usize, 4 as usize])) * 4.0) as i32;
}
function p_void(env: usize, cls: usize, j: usize, obj: usize, m: usize): i32 {
    jni.call_void_method_a(j, obj, m, jni.jvalues([7 as usize]));
    return 0;
}
function main(): i32 { return 0; }`
	exps := []string{"p_int", "p_dbl", "p_void"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	elfExports := make([]nativeelf.Export, len(exps))
	for i, n := range exps {
		elfExports[i] = nativeelf.Export{Name: n, Value: ev[n]}
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs), elfExports, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// table[51]=CallIntMethodA (sum of 3 jvalue ints), [60]=CallDoubleMethodA
	// ((a0+a1)+0.25, FP return), [63]=CallVoidMethodA (records its arg).
	loader := `#include <dlfcn.h>
#include <stdio.h>
static long cim(void*e,long o,long m,long long* a){(void)e;(void)o;(void)m; return (long)(a[0]+a[1]+a[2]); }
static double cdm(void*e,long o,long m,long long* a){(void)e;(void)o;(void)m; return (double)(a[0]+a[1]) + 0.25; }
static long g_void;
static long cvm(void*e,long o,long m,long long* a){(void)e;(void)o;(void)m; g_void=(long)a[0]; return 0; }
int main(int c,char**v){
  void*h=dlopen(v[1],RTLD_NOW);if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void*t[256]={0}; t[51]=(void*)cim; t[60]=(void*)cdm; t[63]=(void*)cvm;
  void*tp=t; void*env=&tp; long L=(long)env;
  if(((int(*)(long,long,long,long,long))dlsym(h,"p_int"))(0,0,L,0,0) != 42) return 1;
  if(((int(*)(long,long,long,long,long))dlsym(h,"p_dbl"))(0,0,L,0,0) != 29) return 2;
  ((int(*)(long,long,long,long,long))dlsym(h,"p_void"))(0,0,L,0,0);
  if(g_void != 7) return 3;
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
		t.Fatalf("Call*MethodA dispatch = %d, want 42 (out=%q)", code, out)
	}
}

// TestStdJNIJvalueFloatArgs validates passing float/double method ARGUMENTS
// via the typed jvalue setters: jvalue_alloc + jvalue_set_f64/int/f32 build
// a mixed-type arg array whose slots carry IEEE-754 bits (jdouble = 8 bytes,
// jfloat = low 4 bytes) and integer words, exactly as the jvalue union lays
// out. A fake CallDoubleMethodA reads d/i/f back out and sums them.
func TestStdJNIJvalueFloatArgs(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function p(env: usize, cls: usize, j: usize, obj: usize, m: usize): i32 {
    var a: usize = jni.jvalue_alloc(3);
    a = jni.jvalue_set_f64(a, 0, 2.5);
    a = jni.jvalue_set_int(a, 1, 4 as usize);
    a = jni.jvalue_set_f32(a, 2, 1.5 as f32);
    return (jni.call_double_method_a(j, obj, m, a) * 4.0) as i32;
}
function main(): i32 { return 0; }`
	exps := []string{"p"}
	asm := compileToX86AsmExports(t, src, exps)
	text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exps)
	if err != nil {
		t.Fatalf("AssembleProgramShared: %v", err)
	}
	so := nativeelf.SharedLibraryX86(text, rodata, toElfRelocsX86(relocs),
		[]nativeelf.Export{{Name: "p", Value: ev["p"]}}, "libfern.so")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	// jvalue union: [0].d (double @0), [1].i (int @ slot+0 = byte 8),
	// [2].f (float @ slot+0 = byte 16). Sum = 2.5 + 4 + 1.5 = 8.0; ×4 = 32.
	loader := `#include <dlfcn.h>
#include <stdio.h>
#include <string.h>
static double cdm(void*e,long o,long m,unsigned char* a){
  (void)e;(void)o;(void)m;
  double d; memcpy(&d, a+0, 8);
  int i;    memcpy(&i, a+8, 4);
  float f;  memcpy(&f, a+16, 4);
  return d + (double)i + (double)f;
}
int main(int c,char**v){
  void*h=dlopen(v[1],RTLD_NOW);if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  void*t[256]={0}; t[60]=(void*)cdm; void*tp=t; void*env=&tp; long L=(long)env;
  if(((int(*)(long,long,long,long,long))dlsym(h,"p"))(0,0,L,0,0) != 32) return 1;
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
		t.Fatalf("jvalue float-arg packing = %d, want 42 (out=%q)", code, out)
	}
}
