package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Every indirect call the arm64 backend emits: a closure, a top-level
// function passed as a value, a combinator calling its element function and
// dyn dispatch.
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

// TestSelfHostHardenArm64 pins -harden on arm64-linux: the image carries the
// GNU property note asking for BTI, the program runs with the kernel
// enforcing it, and the landing pads are what keeps it running — the same
// image with its pads turned into NOPs dies on the first indirect call.
func TestSelfHostHardenArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	armgcc, qemu := arm64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHardenSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(out string, flags ...string) {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "arm64-linux", "-o", out, src, cli.stdlib)
		if msg, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
		if err := os.Chmod(out, 0o755); err != nil && !strings.HasSuffix(out, ".s") {
			t.Fatal(err)
		}
	}
	enforced := btiEnforced(qemu)

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
		if !enforced {
			t.Log("this host does not enforce BTI; the pads are not proved load-bearing here")
			return
		}
		nopad := filepath.Join(dir, "nopad")
		if err := os.WriteFile(nopad, bytes.ReplaceAll(img, pad, binary.LittleEndian.AppendUint32(nil, 0xd503201f)), 0o755); err != nil {
			t.Fatal(err)
		}
		expectSIGILL(t, qemu, nopad)
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
		if !enforced {
			return
		}
		var kept []string
		for _, line := range strings.Split(string(text), "\n") {
			if strings.TrimSpace(line) != "bti c" {
				kept = append(kept, line)
			}
		}
		expectSIGILL(t, qemu, link("unpadded", []byte(strings.Join(kept, "\n"))))
	})

	t.Run("refusals", func(t *testing.T) {
		for _, args := range [][]string{
			{"-harden", "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(dir, "x.s"), src, cli.stdlib},
			{"-harden", "-shared", "-target", "arm64-linux", "-o", filepath.Join(dir, "x.so"), src, cli.stdlib},
		} {
			msg, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(msg), "-harden is only supported for -target arm64-linux executables") {
				t.Errorf("%v: want the -harden refusal, got err=%v\n%s", args, err, msg)
			}
		}
	})
}

// btiEnforced reports whether a binary run through `qemu` gets BTI enforced:
// qemu's linux-user mode does for an image that asks, and native hardware
// does only from ARMv8.5-A, which the baseline does not require.
func btiEnforced(qemu string) bool {
	if qemu != "" {
		return true
	}
	info, err := os.ReadFile("/proc/cpuinfo")
	return err == nil && bytes.Contains(info, []byte(" bti"))
}

// btiNote reports whether the ELF at path has a PT_GNU_PROPERTY whose
// GNU_PROPERTY_AARCH64_FEATURE_1_AND property sets the BTI bit.
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
	out, err := runArm64Bin(qemu, bin).CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("%s ran without its landing pads (err=%v):\n%s", filepath.Base(bin), err, out)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGILL {
		t.Fatalf("%s without its landing pads: %v, want SIGILL\n%s", filepath.Base(bin), err, out)
	}
}
