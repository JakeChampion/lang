package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const selfHostHardenSrc = `import "std/i32";

trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }

@noinline
function apply(f: (i32) => i32, x: i32): i32 {
  return f(x);
}

@noinline
function twice(x: i32): i32 {
  return x * 2;
}

function main(): i32 {
  let k: i32 = 5;
  let add: (i32) => i32 = (x: i32): i32 => x + k;
  let shapes: dyn Shape[] = [Sq { s: 3 }, Rect { w: 2, h: 4 }];
  let total: i32 = 0;
  for s in shapes {
    total = total + s.area();
  }
  let xs: i32[] = [1, 2, 3].map(twice);
  print(apply(add, 10).to_string() + " " + apply(twice, 7).to_string() + " " + total.to_string() + " " + xs[2].to_string());
  return 0;
}
`

const hardenWant = "15 14 17 6"

const hardenAbortSrc = `@noinline
function pick(xs: i32[], i: i32): i32 {
  return xs[i];
}

@noinline
function outer(xs: i32[], i: i32): i32 {
  return pick(xs, i) + 1;
}

function main(): i32 {
  return outer([1, 2, 3], 7);
}
`

func TestSelfHostHardenArm64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ELF hardening requires a Linux host")
	}
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	armgcc, qemu := arm64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHardenSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(out string, flags ...string) {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "arm64-linux", "-o", out, src, stdlib)
		if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
		if err := os.Chmod(out, 0o755); err != nil && !strings.HasSuffix(out, ".s") {
			t.Fatal(err)
		}
	}
	enforced := cpuEnforces(qemu, " bti")
	authenticated := cpuEnforces(qemu, " paca")
	mode := "native"
	if qemu != "" {
		mode = "QEMU"
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Fatalf("read the kernel version: %v", err)
	}
	t.Logf("compiler host=%s/%s; kernel=%s", runtime.GOOS, runtime.GOARCH, strings.TrimSpace(string(kernel)))
	t.Logf("execution=%s; BTI fault control requested=%t; PAC fault control requested=%t", mode, enforced, authenticated)

	t.Run("in-process", func(t *testing.T) {
		plain := filepath.Join(dir, "plain")
		hard := filepath.Join(dir, "hard")
		compile(plain)
		compile(hard, "-harden")
		if btiNote(t, plain) {
			t.Error("an image built without -harden asks for BTI")
		}
		if !btiNote(t, hard) {
			t.Fatal("the -harden image has no PT_GNU_PROPERTY asking for BTI")
		}
		for _, bin := range []string{plain, hard} {
			if out := runHardened(t, qemu, bin); out != hardenWant {
				t.Errorf("%s printed %q, want %q", filepath.Base(bin), out, hardenWant)
			}
		}
		img, err := os.ReadFile(hard)
		if err != nil {
			t.Fatal(err)
		}
		pad := binary.LittleEndian.AppendUint32(nil, 0xd503245f)
		if bytes.Count(img, pad) < 3 {
			t.Fatalf("the -harden image has %d landing pads, want one per indirect-call target", bytes.Count(img, pad))
		}
		sign := binary.LittleEndian.AppendUint32(nil, 0xd503233f)
		auth := binary.LittleEndian.AppendUint32(nil, 0xd50323bf)
		if bytes.Count(img, sign) == 0 || bytes.Count(img, auth) < bytes.Count(img, sign) {
			t.Fatalf("the -harden image has %d paciasp and %d autiasp, want every framed function to sign and each of its exits to authenticate", bytes.Count(img, sign), bytes.Count(img, auth))
		}
		if !enforced {
			t.Log("this host does not enforce BTI; the pads are not proved load-bearing here")
			return
		}
		nopad := filepath.Join(dir, "nopad")
		if err := os.WriteFile(nopad, bytes.ReplaceAll(img, pad, binary.LittleEndian.AppendUint32(nil, 0xd503201f)), 0o755); err != nil {
			t.Fatal(err)
		}
		expectSIGILL(t, qemu, nopad)
		t.Logf("BTI fault control passed (%s)", mode)
	})

	t.Run("emit-asm", func(t *testing.T) {
		asm := filepath.Join(dir, "hard.s")
		compile(asm, "-harden", "-emit", "asm")
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(text, []byte(".note.gnu.property")) {
			t.Fatal("the -harden text carries no .note.gnu.property")
		}
		link := func(name string, body []byte) string {
			t.Helper()
			s := filepath.Join(dir, name+".s")
			bin := filepath.Join(dir, name)
			if err := os.WriteFile(s, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if msg, err := exec.Command(armgcc, "-nostdlib", "-static", "-o", bin, s).CombinedOutput(); err != nil {
				t.Fatalf("link %s: %v\n%s", name, err, msg)
			}
			return bin
		}
		linked := link("linked", text)
		if !btiNote(t, linked) {
			t.Fatal("the linked -harden image has no PT_GNU_PROPERTY asking for BTI")
		}
		if out := runHardened(t, qemu, linked); out != hardenWant {
			t.Errorf("printed %q, want %q", out, hardenWant)
		}
		if enforced {
			var kept []string
			for _, line := range strings.Split(string(text), "\n") {
				if strings.TrimSpace(line) != "bti c" {
					kept = append(kept, line)
				}
			}
			expectSIGILL(t, qemu, link("unpadded", []byte(strings.Join(kept, "\n"))))
			t.Logf("BTI fault control passed (%s)", mode)
		} else {
			t.Log("BTI enforcement remains unverified on this host")
		}
		if !authenticated {
			t.Log("PAC enforcement remains unverified on this host")
			return
		}
		var kept []string
		for _, line := range strings.Split(string(text), "\n") {
			if strings.TrimSpace(line) == "paciasp" {
				// PACIASP is also a BTI target. Preserve BTI in the PAC control.
				line = "    bti c"
			}
			kept = append(kept, line)
		}
		expectHardenSignal(t, qemu, link("unsigned", []byte(strings.Join(kept, "\n"))), syscall.SIGSEGV, syscall.SIGILL)
		for i, line := range kept {
			if strings.TrimSpace(line) == "autiasp" {
				kept[i] = "    nop"
			}
		}
		if out := runHardened(t, qemu, link("unauthenticated", []byte(strings.Join(kept, "\n")))); out != hardenWant {
			t.Fatalf("the PAC control without authentication printed %q, want %q", out, hardenWant)
		}
		t.Logf("PAC fault control passed (%s)", mode)
	})

	t.Run("backtrace", func(t *testing.T) {
		abort := filepath.Join(dir, "abort.fern")
		if err := os.WriteFile(abort, []byte(hardenAbortSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "abort")
		args := []string{"-harden", "-backtrace", "-target", "arm64-linux", "-o", bin, abort, stdlib}
		if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		out, _ := runArm64Bin(qemu, bin).CombinedOutput()
		frames := 0
		for _, line := range strings.Split(string(out), "\n") {
			hex, ok := strings.CutPrefix(strings.TrimSpace(line), "0x")
			if !ok {
				continue
			}
			frames++
			if v, err := strconv.ParseUint(hex, 16, 64); err != nil || v >= 1<<32 {
				t.Errorf("backtrace frame %q is not a plain code address", line)
			}
		}
		if frames < 2 {
			t.Fatalf("want a backtrace through pick and outer, got:\n%s", out)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		for _, args := range [][]string{
			{"-harden", "-target", "arm64-darwin", "-emit", "asm", "-o", filepath.Join(dir, "x.s"), src, stdlib},
			{"-harden", "-shared", "-target", "arm64-linux", "-o", filepath.Join(dir, "x.so"), src, stdlib},
			{"-harden", "-shared", "-target", "x86-64-linux", "-o", filepath.Join(dir, "x86.so"), src, stdlib},
		} {
			msg, err := exec.Command(cli, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(msg), "-harden is only supported for -target arm64-linux and x86-64-linux executables") {
				t.Errorf("%v: want the -harden refusal, got err=%v\n%s", args, err, msg)
			}
		}
	})
}

func cpuEnforces(qemu, flag string) bool {
	if qemu != "" {
		return true
	}
	info, err := os.ReadFile("/proc/cpuinfo")
	return err == nil && bytes.Contains(info, []byte(flag))
}

func btiNote(t *testing.T, path string) bool {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type != elf.ProgType(0x6474e553) {
			continue
		}
		b := make([]byte, p.Filesz)
		if _, err := p.ReadAt(b, 0); err != nil {
			t.Fatal(err)
		}
		for off := 16; off+12 <= len(b); off += 8 + int((binary.LittleEndian.Uint32(b[off+4:])+7)&^7) {
			if binary.LittleEndian.Uint32(b[off:]) == 0xc0000000 && binary.LittleEndian.Uint32(b[off+8:])&1 != 0 {
				return true
			}
		}
	}
	return false
}

func runHardened(t *testing.T, qemu, bin string) string {
	t.Helper()
	out, err := runArm64Bin(qemu, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", filepath.Base(bin), err, out)
	}
	return strings.TrimSpace(string(out))
}

func expectSIGILL(t *testing.T, qemu, bin string) {
	t.Helper()
	expectHardenSignal(t, qemu, bin, syscall.SIGILL)
}

func expectHardenSignal(t *testing.T, qemu, bin string, signals ...syscall.Signal) {
	t.Helper()
	out, err := runArm64Bin(qemu, bin).CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("%s: err=%v, want signals %v\n%s", filepath.Base(bin), err, signals, out)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if ok && ws.Signaled() {
		for _, signal := range signals {
			if ws.Signal() == signal {
				return
			}
		}
	}
	t.Fatalf("%s: %v, want signals %v\n%s", filepath.Base(bin), err, signals, out)
}
