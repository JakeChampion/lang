// Shared-library (.so) end-to-end tests — slice 2 of the Android app path.
// Compile a Fern program to an x86-64 shared object that EXPORTS a chosen
// function, then dlopen + dlsym + call that export from a gcc-built loader
// on the host. This is the same artifact an Android app would load via
// System.loadLibrary; calling the export proves the dynamic symbol table
// resolves and Fern code runs inside a dlopen'd library (no `_start`, with
// the lazy-mmap heap initialising on first use).
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

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
