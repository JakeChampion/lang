package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The arm64 Linux Image form of an arm64-kernel image (#14767). The header
// gives a text offset of 2 MiB, so a loader puts the Image 2 MiB above the
// start of its RAM and enters it at byte 0 with the device tree address in
// x0. qemu-virt and Firecracker do so, and only for this form do they load
// an initrd and name it in /chosen. qemu-virt has its RAM at 0x40000000, so
// the image takes the same RAM as the ELF form of the other boot tests.
const (
	imageBase = kernelBase
	imageSize = kernelSize
	imageRAM  = kernelRAM
)

// The board of the boot test, with the device tree at the address in x0.
var imageBoardSrc = strings.ReplaceAll(strings.Replace(kernelBoardSrc, "const DEVICE_TREE: usize = 0x40000000;\n", "", 1), "DEVICE_TREE + k", "__boot_info() + k")

const imageMainSrc = `import "./board";
import "std/cpio";
import "std/fdt";
import "std/option";

// The initrd that /chosen names, which must lie above the RAM of the image.
function initrd(t: fdt.Tree): string {
  let Some(c) = t.find_name("chosen") else {
    return "no chosen node";
  };
  let Some(start) = t.property_cells(c, "linux,initrd-start") else {
    return "no initrd";
  };
  let Some(end) = t.property_cells(c, "linux,initrd-end") else {
    return "no initrd end";
  };
  if (start < 0x48000000 as u64) {
    return "the initrd overlaps the RAM of the image";
  }
  let image: u8[] = [];
  unsafe {
    let at = start as usize;
    while (at < end as usize) {
      image = image.append(__load_u8(at) as u8);
      at += 1 as usize;
    }
  }
  let Ok(a) = cpio.parse(image) else {
    return "the initrd is not a cpio archive";
  };
  let Some(motd) = a.file("/etc/motd") else {
    return "the initrd has no /etc/motd";
  };
  unsafe {
    return "initrd " + a.entries.len().to_string() + " entries, motd " + string_from_bytes_unchecked(motd);
  }
}

function main(): i32 {
  print("hello from an Image");
  print("device tree in x0 " + (__boot_info() != 0 as usize).to_string());
  match (fdt.parse(board.device_tree())) {
    Ok(t) => {
      let Some(uart) = t.find_compatible("arm,pl011") else {
        print("no uart");
        board.power_off_hvc();
        return 1;
      };
      print("uart " + t.reg(uart, 0).unwrap_or(fdt.Region { address: 0 as u64, size: 0 as u64 }).address.to_string());
      print(initrd(t));
    },
    Err(e) => {
      print(e.message());
    }
  }
  board.power_off_hvc();
  return 0;
}
`

func writeImageProgram(t *testing.T) string {
	t.Helper()
	return writeTickProgram(t, imageBoardSrc, imageMainSrc)
}

// The Image holds the bytes of each load segment of the ELF form at the
// segment's address minus BASE. Its header branches to the ELF entry, puts
// the image at offset 0 of the RAM, claims the whole -ram range and carries
// the magic.
func TestSelfHostKernelLinuxImageLayout(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeImageProgram(t)
	dir := t.TempDir()
	elfPath := filepath.Join(dir, "kernel.elf")
	imgPath := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", elfPath, src, "-ram", imageRAM).CombinedOutput(); err != nil {
		t.Fatalf("build the ELF: %v\n%s", err, msg)
	}
	if msg, err := cli.compileCmd("arm64-kernel", imgPath, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build the Image: %v\n%s", err, msg)
	}
	img, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(elfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if len(img) < 64 {
		t.Fatalf("the Image has %d bytes", len(img))
	}
	le := binary.LittleEndian
	code0 := le.Uint32(img[0:])
	if code0>>26 != 0b000101 {
		t.Errorf("code0 is %#x, not a b instruction", code0)
	}
	if got, want := uint64(code0&0x3ffffff)*4, f.Entry-imageBase; got != want {
		t.Errorf("code0 branches to offset %#x, want the ELF entry at offset %#x", got, want)
	}
	for _, c := range []struct {
		name   string
		at     int
		got    uint64
		expect uint64
	}{
		{"text_offset", 8, le.Uint64(img[8:]), 2 << 20},
		{"image_size", 16, le.Uint64(img[16:]), imageSize},
		{"flags", 24, le.Uint64(img[24:]), 2},
		{"magic", 56, uint64(le.Uint32(img[56:])), 0x644d5241},
	} {
		if c.got != c.expect {
			t.Errorf("%s at byte %d is %#x, want %#x", c.name, c.at, c.got, c.expect)
		}
	}
	end := 0
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		want := make([]byte, p.Filesz)
		if _, err := p.ReadAt(want, 0); err != nil {
			t.Fatal(err)
		}
		at := int(p.Vaddr - imageBase)
		from := 0
		if at == 0 {
			// The Image header and the cleared program headers take the
			// place of the ELF headers.
			from = 64 + len(f.Progs)*56
			if !bytes.Equal(img[64:from], make([]byte, from-64)) {
				t.Errorf("the bytes after the header up to %#x are not zero", from)
			}
		}
		if at+len(want) > len(img) || !bytes.Equal(img[at+from:at+len(want)], want[from:]) {
			t.Errorf("the segment at %#x is not at offset %#x of the Image", p.Vaddr, at)
		}
		end = at + len(want)
	}
	if len(img) != end {
		t.Errorf("the Image has %d bytes, want %d, the end of the last segment", len(img), end)
	}
}

func TestSelfHostKernelLinuxImageRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeImageProgram(t)
	out := filepath.Join(t.TempDir(), "kernel.img")
	for _, c := range []struct {
		target, ram, want string
	}{
		{"x86-64-kernel", kernelX86RAM, "fern: -emit image is the arm64 Linux Image form of -target arm64-kernel; -target x86-64-kernel has no image form"},
		{"arm64-kernel", "0x40210000,64M", "fern: -emit image needs a BASE of -ram that is a multiple of 2M"},
	} {
		msg, err := cli.compileCmd(c.target, out, src, "-ram", c.ram, "-emit", "image").CombinedOutput()
		if err == nil || !strings.Contains(string(msg), c.want) {
			t.Errorf("-target %s -ram %s -emit image: err %v, output:\n%s\nwant %q", c.target, c.ram, err, msg, c.want)
		}
	}
}

// qemu-virt loads the Image 2 MiB above the start of its RAM, puts the device
// tree address in x0, and loads the initrd at the RAM start plus 128 MiB,
// just above the RAM of the image.
func TestSelfHostKernelLinuxImageOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeImageProgram(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	archive := filepath.Join(dir, "initrd.cpio")
	files := []cpioFile{{"etc", 0o40755, nil}, {"etc/motd", 0o100644, []byte("hello from the initrd")}}
	if err := os.WriteFile(archive, newcArchive(files), 0o644); err != nil {
		t.Fatal(err)
	}
	console, err := bootQemu(t, qemu, "-M", "virt", "-cpu", "cortex-a76", "-m", "256M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", out, "-initrd", archive)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range []string{"hello from an Image\n", "device tree in x0 true\n", fmt.Sprintf("uart %d\n", 0x09000000), "initrd 2 entries, motd hello from the initrd\n"} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
