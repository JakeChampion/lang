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
	if err := os.WriteFile(src, []byte("function answer(): i32 { return 42; }\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	soPath := filepath.Join(dir, "libfern.so")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-shared", "-export", "answer", "-o", soPath, src).CombinedOutput(); err != nil {
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

func TestCLISharedReservedNameExport(t *testing.T) {
	const src = `function mod(x: i32): i32 { return x + 1; }
function near(x: i32): i32 { return x + 2; }
function r16(x: i32): i32 { return x + 3; }
function main(): i32 { return 0; }
`
	exports := []string{"mod", "near", "r16"}
	bin := buildFernCLI(t)

	t.Run("x86_64_dlopen", func(t *testing.T) {
		gcc, err := exec.LookPath("gcc")
		if err != nil {
			t.Skip("gcc not on PATH")
		}
		if runtime.GOARCH != "amd64" {
			t.Skip("host is not amd64")
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		soPath := filepath.Join(dir, "libfern.so")
		if o, err := exec.Command(bin, "-target", "x86-64-linux", "-shared",
			"-export", strings.Join(exports, ","), "-o", soPath, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("-shared build failed: %v\n%s", err, o)
		}
		loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char**v){
  void*h=dlopen(v[1],RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int t=0;
  for(int i=2;i<c;i++){
    int(*f)(int)=(int(*)(int))dlsym(h,v[i]);
    if(!f){fprintf(stderr,"dlsym %s: %s\n",v[i],dlerror());return 101;}
    t+=f(10);
  }
  return t;
}`
		cPath := filepath.Join(dir, "ld.c")
		if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
			t.Fatal(err)
		}
		ld := filepath.Join(dir, "ld")
		if o, err := exec.Command(gcc, cPath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
			t.Fatalf("gcc loader: %v\n%s", err, o)
		}
		cmd := exec.Command(ld, append([]string{soPath}, exports...)...)
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 36 {
			t.Fatalf("dlopen+call reserved-name exports = %d, want 36 (out=%q)", code, out)
		}
	})

	t.Run("arm64_dynsym", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		soPath := filepath.Join(dir, "libfern.so")
		if o, err := exec.Command(bin, "-target", "arm64-linux", "-shared",
			"-export", strings.Join(exports, ","), "-o", soPath, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("-shared build failed: %v\n%s", err, o)
		}
		got := dynsymExports(t, soPath)
		for _, want := range exports {
			if v, ok := got[want]; !ok {
				t.Errorf("export %q missing from .dynsym (got %v)", want, got)
			} else if v == 0 {
				t.Errorf("export %q resolved to vaddr 0", want)
			}
		}
	})
}

func dynsymExports(t *testing.T, soPath string) map[string]uint64 {
	t.Helper()
	raw, err := os.ReadFile(soPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	off := func(vaddr uint64) uint64 {
		for _, p := range f.Progs {
			if p.Type == elf.PT_LOAD && vaddr >= p.Vaddr && vaddr < p.Vaddr+p.Filesz {
				return p.Off + (vaddr - p.Vaddr)
			}
		}
		t.Fatalf("vaddr %#x is in no PT_LOAD segment", vaddr)
		return 0
	}
	var symtab, strtab, hash uint64
	for _, p := range f.Progs {
		if p.Type != elf.PT_DYNAMIC {
			continue
		}
		d := raw[p.Off : p.Off+p.Filesz]
		for i := 0; i+16 <= len(d); i += 16 {
			tag := elf.DynTag(f.ByteOrder.Uint64(d[i:]))
			val := f.ByteOrder.Uint64(d[i+8:])
			switch tag {
			case elf.DT_SYMTAB:
				symtab = val
			case elf.DT_STRTAB:
				strtab = val
			case elf.DT_HASH:
				hash = val
			}
		}
	}
	if symtab == 0 || strtab == 0 || hash == 0 {
		t.Fatalf("PT_DYNAMIC missing DT_SYMTAB/DT_STRTAB/DT_HASH (%#x/%#x/%#x)", symtab, strtab, hash)
	}
	nsym := f.ByteOrder.Uint32(raw[off(hash)+4:])
	out := map[string]uint64{}
	for i := uint64(1); i < uint64(nsym); i++ {
		e := off(symtab) + i*24
		nameOff := uint64(f.ByteOrder.Uint32(raw[e:]))
		value := f.ByteOrder.Uint64(raw[e+8:])
		s := raw[off(strtab)+nameOff:]
		out[string(s[:bytes.IndexByte(s, 0)])] = value
	}
	return out
}

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
		{"arith", `function compute(): i32 { let x = 6; let y = 7; return x * y; }
function main(): i32 { return compute(); }`, 42},
		{"recursion", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1)+fib(n-2); }
function fib10(): i32 { return fib(10); }
function main(): i32 { return fib10(); }`, 55},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			export := c.name
			switch c.name {
			case "const":
				export = "answer"
			case "arith":
				export = "compute"
			case "recursion":
				export = "fib10"
			}
			so := selfHostSharedX86(t, c.src, export)

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
		{"one_arg", "triple", `function triple(x: i32): i32 { return x * 3; }
function main(): i32 { return triple(14); }`, 14, 0, 0, 42},
		{"jni_shape", "jni_answer", `function jni_answer(env: i64, cls: i64, n: i32): i32 { return n + 1; }
function main(): i32 { return 0; }`, 0, 0, 41, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			so := selfHostSharedX86(t, c.src, c.fn)
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

func TestSharedLibX86CCallFFI(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `function run0(env: usize, cls: usize, cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(env: usize, cls: usize, cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run4(env: usize, cls: usize, cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return 0; }`
	exps := []string{"run0", "run1", "run4"}
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
	so := selfHostSharedX86(t, src, "probe1")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestStdJNIStaticIds(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
	so := selfHostSharedX86(t, src, exps...)
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestAsBytesInSharedLib(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `function ab_idx(env: usize, cls: usize, i: usize): i32 {
    let s: string = "the quick brown fox";
    let b: [u8] = s.as_bytes();
    return (b[i as i32] as i32);
}
function main(): i32 { return 0; }`
	so := selfHostSharedX86(t, src, "ab_idx")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int (*pi)(long,long,long) = (int(*)(long,long,long)) dlsym(h, "ab_idx");
  if(!pi) return 101;
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

func TestStdJNIFloatFieldAccessors(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/jni";
function p_float(env: usize, cls: usize, j: usize): i32 { return (jni.get_float_field(j, 0 as usize, 0 as usize) * (4.0 as f32)) as i32; }
function p_double(env: usize, cls: usize, j: usize): i32 { return (jni.get_double_field(j, 0 as usize, 0 as usize) * 4.0) as i32; }
function p_sdouble(env: usize, cls: usize, j: usize): i32 { return (jni.get_static_double_field(j, 0 as usize, 0 as usize) * 4.0) as i32; }
function main(): i32 { return 0; }`
	exps := []string{"p_float", "p_double", "p_sdouble"}
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestStdlibInSharedLib(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	src := `import "std/unicode";
import "std/string";
import "std/base64";
import "std/codec";
import "std/hex";
function check(env: usize, cls: usize): i32 {
    let ok: i32 = 0;
    if (("HELLO").to_lower() == "hello") { ok = ok + 1; }
    if (("hello").to_upper() == "HELLO") { ok = ok + 2; }
    if (base64.encode("Man".bytes()) == "TWFu") { ok = ok + 4; }
    if (string_from_bytes_unchecked(__codec_bytes(base64.decode("TWFu"))) == "Man") { ok = ok + 8; }
    if (hex.encode("AB".bytes()) == "4142") { ok = ok + 16; }
    if (string_from_bytes_unchecked(__codec_bytes(hex.decode("4142"))) == "AB") { ok = ok + 32; }
    return ok;
}
function main(): i32 { return 0; }
// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
}
`
	exps := []string{"check"}
	so := selfHostSharedX86(t, src, exps...)
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
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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
    let a: usize = jni.jvalue_alloc(3);
    a = jni.jvalue_set_f64(a, 0, 2.5);
    a = jni.jvalue_set_int(a, 1, 4 as usize);
    a = jni.jvalue_set_f32(a, 2, 1.5 as f32);
    return (jni.call_double_method_a(j, obj, m, a) * 4.0) as i32;
}
function main(): i32 { return 0; }`
	exps := []string{"p"}
	so := selfHostSharedX86(t, src, exps...)
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
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

func selfHostSharedX86(t *testing.T, src string, exports ...string) []byte {
	t.Helper()
	return selfHostShared(t, "x86-64-linux", src, exports...)
}

func selfHostShared(t *testing.T, target, src string, exports ...string) []byte {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	soPath := filepath.Join(dir, "libfern.so")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-shared", "-export", strings.Join(exports, ","),
		"-o", soPath, srcPath, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host -shared: %v\n%s", err, out)
	}
	so, err := os.ReadFile(soPath)
	if err != nil {
		t.Fatal(err)
	}
	return so
}

const sharedCEntrySrc = `function greet(n: i32): i32 { let s: string = "hello" + "!"; return s.len() + n; }
function neg(x: i32): i32 { return 0 - x; }
function low(b: u8, w: u32): i64 { return (b as i64) * 100000 + (w as i64); }
function scale(x: f64, n: i32): f64 { return x * (n as f64); }
function halve(x: f32): f32 { return x * (0.5 as f32); }
function sum10(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64, g: i64, h: i64, i: i64, j: i64): i64 { return a + b + c + d + e + f + g * 10 + h * 100 + i * 1000 + j * 10000; }
function fsum9(a: f64, b: f64, c: f64, d: f64, e: f64, f: f64, g: f64, h: f64, i: f64): f64 { return a + b + c + d + e + f + g + h * 10.0 + i * 100.0; }
function main(): i32 { return 0; }`

const sharedCEntryLoader = `#include <dlfcn.h>
#include <stdio.h>
int main(int c, char** v){
  void* h = dlopen(v[1], RTLD_NOW); if(!h){fprintf(stderr,"%s\n",dlerror());return 100;}
  int (*greet)(int) = dlsym(h, "greet");
  int (*neg)(int) = dlsym(h, "neg");
  long (*low)(long, long) = dlsym(h, "low");
  double (*scale)(double, int) = dlsym(h, "scale");
  float (*halve)(float) = dlsym(h, "halve");
  long (*sum10)(long,long,long,long,long,long,long,long,long,long) = dlsym(h, "sum10");
  double (*fsum9)(double,double,double,double,double,double,double,double,double) = dlsym(h, "fsum9");
  if(!greet||!neg||!low||!scale||!halve||!sum10||!fsum9) return 101;
  printf("%d %d %d %ld %g %g %ld %g\n", greet(4), greet(-10), neg(-7), low(0x1ff05, 0x100000002), scale(1.5, 4), halve(3.0f),
    sum10(1,2,3,4,5,6,7,8,9,1), fsum9(1,2,3,4,5,6,7,8,9));
  return 0;
}`

const sharedCEntryWant = "10 -4 7 500002 6 1.5 19891 1008\n"

func sharedCEntryCheck(t *testing.T, target, cc, runner string, env []string, machine elf.Machine) {
	t.Helper()
	so := selfHostShared(t, target, sharedCEntrySrc, "greet", "neg", "low", "scale", "halve", "sum10", "fsum9")
	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	cPath := filepath.Join(dir, "loader.c")
	if err := os.WriteFile(cPath, []byte(sharedCEntryLoader), 0o644); err != nil {
		t.Fatal(err)
	}
	ld := filepath.Join(dir, "loader")
	if out, err := exec.Command(cc, cPath, "-ldl", "-o", ld).CombinedOutput(); err != nil {
		t.Fatalf("%s loader: %v\n%s", cc, err, out)
	}
	cmd := exec.Command(ld, soPath)
	if runner != "" {
		cmd = exec.Command(runner, ld, soPath)
	}
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("loader: %v\n%s", err, out)
	}
	if got := string(out); got != sharedCEntryWant {
		t.Errorf("exports answered %q, want %q", got, sharedCEntryWant)
	}
	sharedImageCheck(t, so, machine)
}

func sharedImageCheck(t *testing.T, so []byte, machine elf.Machine) {
	t.Helper()
	f, err := elf.NewFile(bytes.NewReader(so))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != elf.ET_DYN || f.Machine != machine {
		t.Errorf("type/machine = %v/%v, want ET_DYN/%v", f.Type, f.Machine, machine)
	}
	var dynamic, stack bool
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_DYNAMIC:
			dynamic = true
		case elf.PT_GNU_STACK:
			stack = p.Flags&elf.PF_X == 0
		case elf.PT_LOAD:
			if machine == elf.EM_AARCH64 && p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
				t.Errorf("a PT_LOAD at %#x is writable and executable", p.Vaddr)
			}
		}
	}
	if !dynamic || !stack {
		t.Errorf("PT_DYNAMIC present = %v, non-executable PT_GNU_STACK = %v; a loader needs both", dynamic, stack)
	}
	if len(so) > 256<<10 {
		t.Errorf("a seven-function library is %d bytes; its .bss is being written into the file", len(so))
	}
	if bytes.Contains(so, []byte("fern: stack overflow")) {
		t.Errorf("the shared object carries the stack-overflow handler, which would take its host's SIGSEGV")
	}
}

func TestSelfHostSharedCEntry(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64")
	}
	sharedCEntryCheck(t, "x86-64-linux", gcc, "", nil, elf.EM_X86_64)
}

func TestArm64SelfHostSharedCEntry(t *testing.T) {
	gcc, qemu := e2eharness.Arm64Tooling(t)
	var env []string
	if qemu != "" {
		env = []string{"QEMU_LD_PREFIX=/usr/aarch64-linux-gnu"}
	}
	sharedCEntryCheck(t, "arm64-linux", gcc, qemu, env, elf.EM_AARCH64)
}

func TestArm64SelfHostSharedAndroid(t *testing.T) {
	so := selfHostShared(t, "arm64-android", "function answer(): i32 { return 42; }\nfunction main(): i32 { return answer(); }\n", "answer")
	sharedImageCheck(t, so, elf.EM_AARCH64)
	if !bytes.Contains(so, []byte("answer\x00")) {
		t.Errorf(".dynstr does not contain the export name")
	}
}

func TestSelfHostSharedRefusals(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function id[T](x: T): T { return x; }\nfunction answer(): i32 { return 42; }\nfunction main(): i32 { return id(answer()); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	so := filepath.Join(dir, "lib.so")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no_output", []string{"-shared"}, "fern: -shared requires -o OUTPUT.so"},
		{"wasm", []string{"-shared", "-target", "wasm32-wasi", "-o", so}, `fern: -shared is only supported with -target x86-64-linux, arm64-linux or arm64-android (got "wasm32-wasi")`},
		{"missing", []string{"-shared", "-export", "answer,nope", "-o", so}, "fern: -export nope: the program has no top-level function nope"},
		{"generic", []string{"-shared", "-export=id", "-o", so}, "fern: -export id: a generic function has no single body to export"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(e2eharness.SelfHostCLI(t), append(c.args, src, e2eharness.SelfHostStdlibRoot(t))...)
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("fern %s succeeded; want the refusal %q", strings.Join(c.args, " "), c.want)
			}
			if got := strings.TrimSpace(string(out)); got != c.want {
				t.Errorf("stderr = %q, want %q", got, c.want)
			}
		})
	}
}
