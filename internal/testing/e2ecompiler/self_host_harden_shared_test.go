package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// answer allocates on each call and calls through a function value, so the
// free lists, their encoded links and the indirect-call check all run.
const hardenSharedSrc = `function twice(x: i32): i32 { return x * 2; }
@noinline function pick(k: i32): (i32) => i32 {
  if (k > 100) { return (y: i32): i32 => y + k; }
  return twice;
}
function answer(a: i32, b: i32): i32 {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 50) { xs = xs.append(i); i = i + 1; }
  let f: (i32) => i32 = pick(a);
  return f(b) + xs[49] - 49;
}
function main(): i32 { return 0; }
`

// The host calls the export at the byte offset in argv[2], twice.
const hardenSharedHost = `#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
int main(int c, char **v) {
  void *h = dlopen(v[1], RTLD_NOW);
  if (!h) { fprintf(stderr, "%s\n", dlerror()); return 100; }
  char *f = (char *)dlsym(h, "answer");
  if (!f) { return 101; }
  int (*g)(int, int) = (int (*)(int, int))(f + atoi(v[2]));
  int first = g(1, 21);
  int second = g(200, 1);
  printf("%d %d\n", first, second);
  return 0;
}
`

const hardenSharedWant = "42 201"

// A shared object has no startup, so its first export call reads the
// canary. Each target builds a hardened object, checks its export entry,
// and loads it into a C host.
func TestSelfHostHardenShared(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux shared objects require a Linux host")
	}
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(src, []byte(hardenSharedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(dir, "host.c")
	if err := os.WriteFile(host, []byte(hardenSharedHost), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(t *testing.T, target, out string, flags ...string) []byte {
		t.Helper()
		args := append(append([]string{}, flags...), "-shared", "-export", "answer", "-target", target, "-o", out, src, stdlib)
		if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", args, err, msg)
		}
		if strings.HasSuffix(out, ".s") {
			text, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			return text
		}
		return exportEntry(t, out)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		if runtime.GOARCH != "amd64" {
			t.Skip("the x86-64 object loads into a native host only")
		}
		gcc, _ := x86_64Tooling(t)
		entry := compile(t, "x86-64-linux", filepath.Join(dir, "x86.so"), "-harden")
		if !bytes.HasPrefix(entry, []byte{0xf3, 0x0f, 0x1e, 0xfa}) {
			t.Fatalf("the hardened export starts with % x, want endbr64", entry[:4])
		}
		if plain := compile(t, "x86-64-linux", filepath.Join(dir, "x86-plain.so")); bytes.HasPrefix(plain, []byte{0xf3, 0x0f, 0x1e, 0xfa}) {
			t.Error("an export built without -harden starts with endbr64")
		}
		text := compile(t, "x86-64-linux", filepath.Join(dir, "x86.s"), "-harden", "-emit", "asm")
		if !bytes.Contains(text, []byte("    call __fern_harden_start\n")) || !bytes.Contains(text, []byte("\n__fern_harden_start:\n")) {
			t.Fatal("the hardened export does not read the canary on its first call")
		}
		bin := filepath.Join(dir, "host-x86")
		if msg, err := exec.Command(gcc, "-o", bin, host, "-ldl").CombinedOutput(); err != nil {
			t.Fatalf("build the host: %v\n%s", err, msg)
		}
		if out := hardenSharedRun(t, exec.Command(bin, filepath.Join(dir, "x86.so"), "0")); out != hardenSharedWant {
			t.Fatalf("the host printed %q, want %q", out, hardenSharedWant)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		so := filepath.Join(dir, "arm64.so")
		plain := filepath.Join(dir, "arm64-plain.so")
		entry := compile(t, "arm64-linux", so, "-harden")
		compile(t, "arm64-linux", plain)
		if btiNote(t, plain) {
			t.Error("a shared object built without -harden asks for BTI")
		}
		if !btiNote(t, so) {
			t.Fatal("the -harden shared object has no PT_GNU_PROPERTY asking for BTI")
		}
		pad, sign := uint32(0xd503245f), uint32(0xd503233f)
		if binary.LittleEndian.Uint32(entry) != pad || binary.LittleEndian.Uint32(entry[4:]) != sign {
			t.Fatalf("the hardened export starts with % x, want bti c and paciasp", entry[:8])
		}
		bin := filepath.Join(dir, "host-arm64")
		if msg, err := exec.Command(armgcc, "-mbranch-protection=standard", "-o", bin, host, "-ldl").CombinedOutput(); err != nil {
			t.Fatalf("build the host: %v\n%s", err, msg)
		}
		run := func(so, off string) *exec.Cmd {
			cmd := runArm64Bin(qemu, bin, so, off)
			if qemu != "" {
				cmd.Env = append(os.Environ(), "QEMU_LD_PREFIX=/usr/aarch64-linux-gnu")
			}
			return cmd
		}
		if out := hardenSharedRun(t, run(so, "0")); out != hardenSharedWant {
			t.Fatalf("the host printed %q, want %q", out, hardenSharedWant)
		}
		if !cpuEnforces(qemu, " bti") {
			t.Log("this host does not enforce BTI; the export's pad is not proved load-bearing here")
			return
		}
		// A call past the pad and paciasp must fault on BTI alone, so the
		// control turns PAC into nop and compares the image with and
		// without its property header.
		img, err := os.ReadFile(so)
		if err != nil {
			t.Fatal(err)
		}
		nop := binary.LittleEndian.AppendUint32(nil, 0xd503201f)
		img = bytes.ReplaceAll(img, binary.LittleEndian.AppendUint32(nil, sign), nop)
		img = bytes.ReplaceAll(img, binary.LittleEndian.AppendUint32(nil, 0xd50323bf), nop)
		guarded := filepath.Join(dir, "guarded.so")
		if err := os.WriteFile(guarded, img, 0o644); err != nil {
			t.Fatal(err)
		}
		unguarded := filepath.Join(dir, "unguarded.so")
		if err := os.WriteFile(unguarded, dropProperty(t, img), 0o644); err != nil {
			t.Fatal(err)
		}
		if out := hardenSharedRun(t, run(unguarded, "8")); out != hardenSharedWant {
			t.Fatalf("a call past the pad of an object with no property printed %q, want %q", out, hardenSharedWant)
		}
		cmd := run(guarded, "8")
		out, err := cmd.CombinedOutput()
		if err == nil || cmd.ProcessState.ExitCode() != 132 && !strings.Contains(string(out), "signal 4") {
			t.Fatalf("a call past the pad of the guarded object: %v\n%s\nwant SIGILL", err, out)
		}
		t.Logf("shared-object BTI fault control passed (qemu=%t)", qemu != "")
	})
}

func hardenSharedRun(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", cmd.Args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// exportEntry returns the first eight bytes of the object's answer export.
func exportEntry(t *testing.T, so string) []byte {
	t.Helper()
	f, err := elf.Open(so)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name != "answer" {
			continue
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_LOAD && s.Value >= p.Vaddr && s.Value+8 <= p.Vaddr+p.Filesz {
				b := make([]byte, 8)
				if _, err := p.ReadAt(b, int64(s.Value-p.Vaddr)); err != nil {
					t.Fatal(err)
				}
				return b
			}
		}
	}
	t.Fatalf("%s has no answer export in a load segment", so)
	return nil
}

// dropProperty turns the image's PT_GNU_PROPERTY header into PT_NULL.
func dropProperty(t *testing.T, img []byte) []byte {
	t.Helper()
	out := append([]byte(nil), img...)
	phoff := binary.LittleEndian.Uint64(out[32:])
	phnum := int(binary.LittleEndian.Uint16(out[56:]))
	for i := 0; i < phnum; i++ {
		at := int(phoff) + i*56
		if binary.LittleEndian.Uint32(out[at:]) == 0x6474e553 {
			binary.LittleEndian.PutUint32(out[at:], 0)
			return out
		}
	}
	t.Fatal("the image has no PT_GNU_PROPERTY header")
	return nil
}
