package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A root that outlives the kernel (#15078). The first boot finds no archive on
// the disk: a process writes a file, and the kernel writes the root of the
// process, the initrd and the layer in memory, to the disk. The second boot
// finds the archive, takes it as the root, and runs a process that reads the
// file. The initrd of the second boot does not have the file.
var saveMainSrc = strings.Replace(processKernelSrc, "import \"std/virtio_rng\";\n", "import \"std/virtio_rng\";\nimport \"std/virtio_blk\";\n", 1) + `
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

// Writes the root of p to the start of the disk, in whole sectors.
function save(blk: virtio_blk.Block, p: user_process.Process): string {
  let image = p.image();
  let size = image.len();
  while (image.len() % virtio_blk.SECTOR_BYTES as i32 != 0) {
    image = image.append(0 as u8);
  }
  if (image.len() as u64 > blk.sectors * virtio_blk.SECTOR_BYTES as u64) {
    return "the root does not fit on the disk";
  }
  if (!blk.write(0 as u64, image) || !blk.flush()) {
    return "the write failed";
  }
  return "saved the root, " + size.to_string() + " bytes";
}

function main(): i32 {
  let Some(b) = boot(8192 as usize) else {
    print("no boot");
    return 1;
  };
  let Ok(t) = fdt.parse(board.device_tree()) else {
    print("no device tree");
    return 1;
  };
  let Some(d) = find_blk(t) else {
    print("no virtio-blk device");
    return 1;
  };
  let Some(blk) = virtio_blk.open(d, b.f, b.kernel) else {
    print("virtio-blk did not open");
    return 1;
  };
  let Some(disk) = blk.read(0 as u64, blk.sectors as i32) else {
    print("the read failed");
    return 1;
  };
  let free = b.f.free_count();
  if let Ok(saved) = cpio.parse(disk) {
    print("booted from the disk, " + saved.entries.len().to_string() + " entries");
    let root = Boot { ...b, a: saved };
    let Some(p) = load(root, "/bin/reader", ["/bin/reader"]) else {
      return 1;
    };
    release(root, "/bin/reader", run_to_end("/bin/reader", p), free);
  } else {
    print("no root on the disk");
    let Some(p) = load(b, "/bin/writer", ["/bin/writer"]) else {
      return 1;
    };
    let q = run_to_end("/bin/writer", p);
    print(save(blk, q));
    release(b, "/bin/writer", q, free);
  }
  board.power_off(0);
  return 0;
}
`

const saveWriterSrc = `function main(): i32 {
  match (create_dir_all("/data")) {
    Ok(_) => {},
    Err(e) => {
      print("mkdir failed");
      return 1;
    }
  }
  match (write_file("/data/note", "kept by fern")) {
    Ok(_) => {},
    Err(e) => {
      print("write failed");
      return 1;
    }
  }
  print("wrote /data/note");
  return 0;
}
`

const saveReaderSrc = `function main(): i32 {
  match (read_file("/data/note")) {
    Ok(s) => {
      print("read /data/note: " + s);
      return 0;
    },
    Err(e) => {
      print("no /data/note");
      return 1;
    }
  }
  return 1;
}
`

// newcFiles reads the regular files and directories of a newc archive,
// independently of std/cpio. It stops at the trailer.
func newcFiles(t *testing.T, b []byte) map[string]cpioFile {
	t.Helper()
	files := map[string]cpioFile{}
	at := 0
	field := func(i int) int {
		v, err := strconv.ParseUint(string(b[at+6+8*i:at+14+8*i]), 16, 32)
		if err != nil {
			t.Fatalf("field %d of the header at %d: %v", i, at, err)
		}
		return int(v)
	}
	align := func(n int) int { return (n + 3) &^ 3 }
	for {
		if at+110 > len(b) || string(b[at:at+6]) != "070701" {
			t.Fatalf("no newc header at %d", at)
		}
		mode, size, namesize := field(1), field(6), field(11)
		name := string(b[at+110 : at+110+namesize-1])
		data := align(at + 110 + namesize)
		if name == "TRAILER!!!" {
			return files
		}
		files[name] = cpioFile{name, uint32(mode), b[data : data+size]}
		at = align(data + size)
	}
}

func TestSelfHostKernelSavesItsRootOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrd(t, cli, dir, map[string]string{"writer": saveWriterSrc, "reader": saveReaderSrc}, nil)
	src := writeTickProgram(t, processBoardSrc, saveMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	disk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(disk, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	boot := func(want []string) {
		t.Helper()
		args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "256M",
			"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive}
		out, err := bootQemu(t, qemu, append(args, blkDiskArgs(disk)...)...)
		console := string(out)
		if err != nil {
			t.Fatalf("qemu: %v; console:\n%s", err, console)
		}
		for _, w := range want {
			if !strings.Contains(console, w) {
				t.Errorf("console:\n%s\nwant %q", console, w)
			}
		}
	}
	boot([]string{"no root on the disk\n", "/bin/writer fd 1: wrote /data/note\n", "/bin/writer exit 0\n", "saved the root, ", "/bin/writer gave back its frames true\n"})
	img, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	files := newcFiles(t, img)
	initrd, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range newcFiles(t, initrd) {
		if got, ok := files[name]; !ok || got.mode != f.mode || !bytes.Equal(got.data, f.data) {
			t.Errorf("the disk does not have %s of the initrd", name)
		}
	}
	if f := files["data"]; f.mode&0o170000 != 0o40000 {
		t.Errorf("the disk has data with the mode %o, want a directory", f.mode)
	}
	if f := files["data/note"]; f.mode&0o170000 != 0o100000 || string(f.data) != "kept by fern" {
		t.Errorf("the disk has data/note with the mode %o and %q, want a file with %q", f.mode, f.data, "kept by fern")
	}
	boot([]string{fmt.Sprintf("booted from the disk, %d entries\n", len(files)), "/bin/reader fd 1: read /data/note: kept by fern\n", "/bin/reader exit 0\n", "/bin/reader gave back its frames true\n"})
}
