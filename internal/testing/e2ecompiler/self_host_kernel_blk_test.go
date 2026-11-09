package e2ecompiler

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The first filesystem of milestone 5 (#14264): the image reads a cpio
// archive from the virtio-blk disk of the VM over std/virtio_blk, lists it
// with std/cpio, and writes the last sector of the disk back. The x86-64
// image also reads the same archive as the initrd that qemu loads, through
// the module list of the PVH start info.
const blkProbeSrc = `function sum(b: u8[]): i64 {
  let s = 0i64;
  for x in b {
    s += x as i64;
  }
  return s;
}

function kind(e: cpio.Entry): string {
  if (e.is_dir()) {
    return "dir";
  }
  if (e.is_symlink()) {
    return "link";
  }
  return "file";
}

// Prints each entry of the archive in image, and the two files the test puts
// there.
function show_archive(label: string, image: u8[]): string {
  let Ok(a) = cpio.parse(image) else {
    return label + " is not a cpio archive";
  };
  for e in a.entries {
    print(label + " entry " + e.name + " " + kind(e) + " " + e.size.to_string());
  }
  let Some(motd) = a.file("/etc/motd") else {
    return label + " has no /etc/motd";
  };
  let text = "";
  unsafe {
    text = string_from_bytes_unchecked(motd);
  }
  print(label + " motd " + text);
  let Some(blob) = a.file("blob") else {
    return label + " has no blob";
  };
  return label + " blob " + blob.len().to_string() + " sum " + sum(blob).to_string();
}

// The 512 bytes the image writes to the last sector: the text, then the
// bytes 0 to 255 in turn.
function stamp(): u8[] {
  let out: u8[] = [];
  for b in "written by fern".as_bytes() {
    out = out.append(b);
  }
  let i = 0;
  while (out.len() < 512) {
    out = out.append((i & 255) as u8);
    i += 1;
  }
  return out;
}

function probe_disk(blk: virtio_blk.Block): string {
  print("virtio-blk sectors " + blk.sectors.to_string() + " read-only " + blk.read_only.to_string());
  let Some(image) = blk.read(0 as u64, blk.sectors as i32) else {
    return "the read failed";
  };
  print(show_archive("disk", image));
  if (blk.read(blk.sectors, 1).is_some() || blk.write(blk.sectors, stamp()) || blk.write(0 as u64, [1u8])) {
    return "a request past the disk or of part of a sector did not fail";
  }
  let last = blk.sectors - 1 as u64;
  if (!blk.write(last, stamp()) || !blk.flush()) {
    return "the write failed";
  }
  let Some(back) = blk.read(last, 1) else {
    return "the read back failed";
  };
  let want = stamp();
  for i in 0..want.len() {
    if (back[i] != want[i]) {
      return "the read back differs at byte " + i.to_string();
    }
  }
  return "wrote and read back sector " + last.to_string();
}
`

const blkMainSrc = `import "./board";
import "std/cpio";
import "std/fdt";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/virtio";
import "std/virtio_blk";

` + blkProbeSrc + `
// The first virtio-mmio transport of the device tree with a block device.
function find_blk(t: fdt.Tree): Option[virtio.Device] {
  for i in 0..t.nodes.len() {
    for s in t.property_strings(i, "compatible") {
      if (s == "virtio,mmio") {
        let Some(r) = t.reg(i, 0) else {
          continue;
        };
        match (virtio.probe(r.address as usize)) {
          Some(d) => {
            if (d.id == virtio.ID_BLOCK) {
              return Some(d);
            }
          },
          None => {}
        }
      }
    }
  }
  return None;
}

function main(): i32 {
  // The frames start above the bootstrap heap's cursor, and end at the stack.
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    print("no frames");
    return 1;
  };
  let heap_frames = f.free_count() - 256 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  let Ok(t) = fdt.parse(board.device_tree()) else {
    print("no device tree");
    return 1;
  };
  let Some(mem) = t.find_name("memory") else {
    print("no memory node");
    return 1;
  };
  let Some(r) = t.reg(mem, 0) else {
    print("no memory reg");
    return 1;
  };
  let Some(tables) = pagetable.new(f) else {
    print("no root frame");
    return 1;
  };
  if (!tables.map_normal(r.address as usize, r.size as usize) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  let Some(d) = find_blk(t) else {
    print("no virtio-blk device");
    return 1;
  };
  let Some(blk) = virtio_blk.open(d, f, tables) else {
    print("virtio-blk did not open");
    return 1;
  };
  print(probe_disk(blk));
  return 0;
}
`

// The x86-64 board of the DMA test, with the module list of the start info.
const blkX86BoardSrc = dmaX86BoardSrc + `
pub function modules(info: pvh.StartInfo): u8[] {
  return copy(info.modlist_address as usize, info.modules as i32 * pvh.MODULE_ENTRY_BYTES);
}

pub function module_bytes(m: pvh.Module): u8[] {
  return copy(m.address as usize, m.size as i32);
}
`

const blkX86MainSrc = `import "./board";
import "std/cpio";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/pvh";
import "std/virtio";
import "std/virtio_blk";

` + blkProbeSrc + `
// Maps every RAM region of the memory map as ordinary memory, rounded out to
// pages, and the device window as device memory.
function map_all(tables: pagetable.Tables, regions: pvh.MemoryRegion[]): boolean {
  for r in regions {
    if (r.is_ram()) {
      let start = (r.address as usize) - (r.address as usize) % pagetable.PAGE_BYTES;
      let end = (r.address + r.size) as usize;
      let len = end - start + (pagetable.PAGE_BYTES - end % pagetable.PAGE_BYTES) % pagetable.PAGE_BYTES;
      if (!tables.map_normal(start, len)) {
        return false;
      }
    }
  }
  return tables.map_device(board.DEVICES, board.DEVICES_BYTES);
}

// The first of the eight virtio-mmio transports of qemu microvm with a block
// device.
function find_blk(): Option[virtio.Device] {
  for i in 0..8 {
    match (virtio.probe(board.VIRTIO + i as usize * 512 as usize)) {
      Some(d) => {
        if (d.id == virtio.ID_BLOCK) {
          return Some(d);
        }
      },
      None => {}
    }
  }
  return None;
}

// The archive in the first boot module, which must lie outside the RAM that
// the image takes for its frames.
function show_initrd(info: pvh.StartInfo): string {
  let Ok(mods) = pvh.parse_modules(board.modules(info), info.modules) else {
    return "no module list";
  };
  if (mods.len() == 0) {
    return "no initrd";
  }
  let m = mods[0];
  if (m.address < (board.RAM_BASE + board.RAM_SIZE) as u64 && m.address + m.size > board.RAM_BASE as u64) {
    return "the initrd overlaps the RAM of the image";
  }
  return show_archive("initrd", board.module_bytes(m));
}

function main(): i32 {
  // The frames start above the bootstrap heap's cursor, and end at the stack.
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    print("no frames");
    return 1;
  };
  let Ok(info) = pvh.parse(board.start_info()) else {
    print("no start info");
    return 1;
  };
  let Ok(regions) = pvh.parse_memmap(board.memmap(info), info.memmap_entries) else {
    print("no memory map");
    return 1;
  };
  let heap_frames = f.free_count() - 256 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  print(show_initrd(info));
  let Some(tables) = pagetable.new(f) else {
    print("no root frame");
    return 1;
  };
  if (!map_all(tables, regions)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  let Some(d) = find_blk() else {
    print("no virtio-blk device");
    return 1;
  };
  let Some(blk) = virtio_blk.open(d, f, tables) else {
    print("virtio-blk did not open");
    return 1;
  };
  print(probe_disk(blk));
  return 16;
}
`

type cpioFile struct {
	name string
	mode uint32
	data []byte
}

// newcArchive writes files as a cpio archive in the newc format, with the
// trailer, independently of std/cpio.
func newcArchive(files []cpioFile) []byte {
	var b bytes.Buffer
	pad := func() {
		for b.Len()%4 != 0 {
			b.WriteByte(0)
		}
	}
	add := func(ino int, f cpioFile) {
		fmt.Fprintf(&b, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
			ino, f.mode, 0, 0, 1, 0, len(f.data), 0, 0, 0, 0, len(f.name)+1, 0)
		b.WriteString(f.name)
		b.WriteByte(0)
		pad()
		b.Write(f.data)
		pad()
	}
	for i, f := range files {
		add(i+1, f)
	}
	add(0, cpioFile{name: "TRAILER!!!"})
	return b.Bytes()
}

// blkImage writes the archive and a disk that holds it, padded to whole
// sectors, with 8 more sectors for the image to write. It gives the paths,
// the number of sectors and the lines the image prints for the archive.
func blkImage(t *testing.T) (archive, disk string, sectors int, want []string) {
	t.Helper()
	blob := make([]byte, 100000)
	sum := 0
	for i := range blob {
		blob[i] = byte(i*7 + 3)
		sum += int(blob[i])
	}
	files := []cpioFile{
		{"etc", 0o40755, nil},
		{"etc/motd", 0o100644, []byte("hello from the disk")},
		{"motd", 0o120777, []byte("etc/motd")},
		{"blob", 0o100644, blob},
	}
	a := newcArchive(files)
	sectors = (len(a)+511)/512 + 8
	img := make([]byte, sectors*512)
	copy(img, a)
	dir := t.TempDir()
	archive = filepath.Join(dir, "initrd.cpio")
	disk = filepath.Join(dir, "disk.img")
	if err := os.WriteFile(archive, a, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, img, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"disk", "initrd"} {
		want = append(want,
			label+" entry etc dir 0\n",
			label+" entry etc/motd file 19\n",
			label+" entry motd link 8\n",
			label+" entry blob file 100000\n",
			label+" motd hello from the disk\n",
			fmt.Sprintf("%s blob 100000 sum %d\n", label, sum))
	}
	return archive, disk, sectors, want
}

func blkDiskArgs(disk string) []string {
	return []string{"-global", "virtio-mmio.force-legacy=false",
		"-drive", "file=" + disk + ",if=none,format=raw,id=d0", "-device", "virtio-blk-device,drive=d0"}
}

// checkBlkConsole checks the lines of the console, and the last sector of the
// disk, which the image wrote.
func checkBlkConsole(t *testing.T, console []byte, disk string, sectors int, want []string) {
	t.Helper()
	want = append(want,
		fmt.Sprintf("virtio-blk sectors %d read-only false\n", sectors),
		fmt.Sprintf("wrote and read back sector %d\n", sectors-1))
	for _, w := range want {
		if !strings.Contains(string(console), w) {
			t.Errorf("console:\n%s\nwant %q", console, w)
		}
	}
	img, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	stamp := []byte("written by fern")
	for i := 0; len(stamp) < 512; i++ {
		stamp = append(stamp, byte(i))
	}
	if last := img[len(img)-512:]; !bytes.Equal(last, stamp) {
		t.Errorf("the last sector of the disk is %q, want the stamp of the image", last[:32])
	}
}

func TestSelfHostKernelVirtioBlkOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, blkMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	_, disk, sectors, want := blkImage(t)
	args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", out}
	console, err := bootQemu(t, qemu, append(args, blkDiskArgs(disk)...)...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	// The arm64 image reads only the disk: qemu-virt loads no initrd for an
	// ELF image.
	checkBlkConsole(t, console, disk, sectors, want[:len(want)/2])
}

// main returns 16, so qemu exits with 33 from isa-debug-exit. qemu puts the
// initrd at the top of the guest RAM, above the RAM that -ram gives the image.
func TestSelfHostKernelX86VirtioBlkOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, blkX86BoardSrc, blkX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	archive, disk, sectors, want := blkImage(t)
	args := []string{"-M", "microvm,acpi=off", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh", "-initrd", archive}
	console, err := bootQemu(t, qemu, append(args, blkDiskArgs(disk)...)...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	checkBlkConsole(t, console, disk, sectors, want)
}
