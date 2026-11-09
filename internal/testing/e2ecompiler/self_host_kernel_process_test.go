package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Milestone 4 of docs/BARE-METAL-PLAN.md (#14263), its second slice: the
// kernel loads static arm64-linux programs from the initrd with
// std/user_process, each in its own page tables, and runs them at EL0.
var processBoardSrc = strings.ReplaceAll(strings.Replace(dmaBoardSrc, "const DEVICE_TREE: usize = 0x40000000;\n", "", 1), "DEVICE_TREE + k", "__boot_info() + k")

const processMainSrc = `import "./board";
import "std/cpio";
import "std/fdt";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/user_process";

function initrd(t: fdt.Tree): Option[u8[]] {
  let Some(c) = t.find_name("chosen") else {
    return None;
  };
  let Some(start) = t.property_cells(c, "linux,initrd-start") else {
    return None;
  };
  let Some(end) = t.property_cells(c, "linux,initrd-end") else {
    return None;
  };
  let image: u8[] = [];
  unsafe {
    let at = start as usize;
    while (at < end as usize) {
      image = image.append(__load_u8(at) as u8);
      at += 1 as usize;
    }
  }
  return Some(image);
}

// Runs the program at path with its own tables, which hold the kernel
// mapping, and prints each line that it writes. At the end the kernel makes
// its own tables active again and gives back the frames of the process.
function spawn(f: frames.Frames, kernel: pagetable.Tables, ram: fdt.Region, a: cpio.Archive, path: string, argv: string[]): void {
  let free = f.free_count();
  run(f, kernel, ram, a, path, argv);
  print(path + " gave back its frames " + (f.free_count() == free).to_string());
}

function run(f: frames.Frames, kernel: pagetable.Tables, ram: fdt.Region, a: cpio.Archive, path: string, argv: string[]): void {
  let Some(image) = a.file(path) else {
    print(path + ": not in the initrd");
    return;
  };
  let Some(tables) = pagetable.new(f) else {
    print("no frame for the tables");
    return;
  };
  if (!tables.map_normal(ram.address as usize, ram.size as usize) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    print("no kernel mapping");
    return;
  }
  let p = match (user_process.load(f, tables, image, argv, ["HOME=/"])) {
    Ok(loaded) => loaded,
    Err(why) => {
      print(path + ": " + why);
      return;
    }
  };
  let line: u8[] = [];
  while (true) {
    let (q, stop) = p.run();
    p = q;
    match (stop) {
      Output(fd, bytes) => {
        for b in bytes {
          if (b == 10 as u8) {
            unsafe {
              print(path + " fd " + fd.to_string() + ": " + string_from_bytes_unchecked(line));
            }
            line = [];
          } else {
            line = line.append(b);
          }
        }
      },
      Exited(status) => {
        print(path + " exit " + status.to_string());
        break;
      },
      Killed(why) => {
        print(path + " killed: " + why);
        break;
      },
      Interrupted => {}
    }
  }
  kernel.activate();
  p.release();
}

function main(): i32 {
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    print("no frames");
    return 1;
  };
  let heap_frames = 4096 as usize;
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
    return 1;
  };
  let Some(ram) = t.reg(mem, 0) else {
    return 1;
  };
  let Some(bytes) = initrd(t) else {
    print("no initrd");
    return 1;
  };
  let Ok(a) = cpio.parse(bytes) else {
    print("the initrd is not a cpio archive");
    return 1;
  };
  let Some(kernel) = pagetable.new(f) else {
    return 1;
  };
  if (!kernel.map_normal(ram.address as usize, ram.size as usize) || !kernel.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    print("no kernel mapping");
    return 1;
  }
  kernel.activate();
  spawn(f, kernel, ram, a, "/bin/hello", ["/bin/hello", "world"]);
  spawn(f, kernel, ram, a, "/bin/efault", ["/bin/efault"]);
  spawn(f, kernel, ram, a, "/bin/crash", ["/bin/crash"]);
  board.power_off(0);
  return 0;
}
`

// hello reads its argument, fills the 16 GiB heap reservation of the runtime
// page by page, and recurses deep into the 1 GiB stack reservation.
const processHelloSrc = `function depth(n: i32): i32 {
  if (n == 0) {
    return 0;
  }
  return 1 + depth(n - 1);
}

function main(): i32 {
  let argv = args();
  print("hello " + argv[1] + " from EL0");
  let parts: string[] = [];
  for i in 0..2000 {
    parts = parts.append(i.to_string());
  }
  print("parts " + parts.len().to_string() + ", last " + parts[1999] + ", depth " + depth(20000).to_string());
  return 3;
}
`

const processCrashSrc = `function main(): i32 {
  print("crash loads a kernel address");
  unsafe {
    return __load_i64(0x40000000 as usize) as i32;
  }
}
`

// efaultImage is a static arm64 ELF of one segment: a write from a kernel
// address, then exit_group with the negated result, so the status is EFAULT.
func efaultImage() []byte {
	code := []uint32{
		0xd2800020, // movz x0, #1
		0xd2a80001, // movz x1, #0x4000, lsl #16
		0xd28000a2, // movz x2, #5
		0xd2800808, // movz x8, #64
		0xd4000001, // svc #0
		0xcb0003e0, // neg x0, x0
		0xd2800bc8, // movz x8, #94
		0xd4000001, // svc #0
	}
	const base, headers = 0x400000, 64 + 56
	le := binary.LittleEndian
	b := make([]byte, headers+4*len(code))
	copy(b, "\x7fELF\x02\x01\x01")
	le.PutUint16(b[16:], 2)   // ET_EXEC
	le.PutUint16(b[18:], 183) // EM_AARCH64
	le.PutUint32(b[20:], 1)
	le.PutUint64(b[24:], base+headers)
	le.PutUint64(b[32:], 64)
	le.PutUint16(b[52:], 64)
	le.PutUint16(b[54:], 56)
	le.PutUint16(b[56:], 1)
	ph := b[64:]
	le.PutUint32(ph[0:], 1) // PT_LOAD
	le.PutUint32(ph[4:], 5) // R X
	le.PutUint64(ph[16:], base)
	le.PutUint64(ph[24:], base)
	le.PutUint64(ph[32:], uint64(len(b)))
	le.PutUint64(ph[40:], uint64(len(b)))
	le.PutUint64(ph[48:], 0x1000)
	for i, w := range code {
		le.PutUint32(b[headers+4*i:], w)
	}
	return b
}

func TestSelfHostKernelProcessOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	programs := map[string][]byte{"bin/efault": efaultImage()}
	for name, src := range map[string]string{"hello": processHelloSrc, "crash": processCrashSrc} {
		path := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, name)
		if msg, err := cli.compileCmd("arm64-linux", out, path).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		programs["bin/"+name] = b
	}
	files := []cpioFile{{"bin", 0o40755, nil}}
	for _, name := range []string{"bin/crash", "bin/efault", "bin/hello"} {
		files = append(files, cpioFile{name, 0o100755, programs[name]})
	}
	archive := filepath.Join(dir, "initrd.cpio")
	if err := os.WriteFile(archive, newcArchive(files), 0o644); err != nil {
		t.Fatal(err)
	}
	src := writeTickProgram(t, processBoardSrc, processMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "256M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	// The data abort from EL0 has the class 0x24, and FAR_EL1 holds the
	// kernel address.
	for _, want := range []string{
		"/bin/hello fd 1: hello world from EL0\n",
		"/bin/hello fd 1: parts 2000, last 1999, depth 20000\n",
		"/bin/hello exit 3\n",
		"/bin/efault exit 14\n",
		"/bin/crash fd 1: crash loads a kernel address\n",
		fmt.Sprintf("/bin/crash killed: a fault of class 36 at %d from ", 0x40000000),
		"/bin/hello gave back its frames true\n",
		"/bin/efault gave back its frames true\n",
		"/bin/crash gave back its frames true\n",
	} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
