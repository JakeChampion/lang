package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Milestone 4 of docs/BARE-METAL-PLAN.md (#14263), its second slice: the
// kernel loads static arm64-linux programs from the initrd with
// std/user_process, each in its own page tables, and runs them at EL0.
var processBoardSrc = strings.ReplaceAll(strings.Replace(dmaBoardSrc, "const DEVICE_TREE: usize = 0x40000000;\n", "", 1), "DEVICE_TREE + k", "__boot_info() + k")

// processKernelSrc is the part of a kernel that starts processes: it moves
// the heap onto frames, reads the initrd, makes the kernel mapping active,
// and loads a program from the initrd into tables of its own.
const processKernelSrc = `import "./board";
import "std/cpio";
import "std/fdt";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/user_process";
import "std/virtio";
import "std/virtio_rng";

struct Boot { f: frames.Frames, ram: fdt.Region, a: cpio.Archive, kernel: pagetable.Tables, rng: Option[virtio_rng.Rng] }

struct Range { start: usize, end: usize }

// The initrd that /chosen names.
function initrd(t: fdt.Tree): Option[Range] {
  let Some(c) = t.find_name("chosen") else {
    return None;
  };
  let Some(start) = t.property_cells(c, "linux,initrd-start") else {
    return None;
  };
  let Some(end) = t.property_cells(c, "linux,initrd-end") else {
    return None;
  };
  return Some(Range { start: start as usize, end: end as usize });
}

function copy_range(r: Range): u8[] {
  let image: u8[] = [];
  unsafe {
    let at = r.start;
    while (at < r.end) {
      image = image.append(__load_u8(at) as u8);
      at += 1 as usize;
    }
  }
  return image;
}

// The Unix time of the PL031 real-time clock of qemu-virt, in nanoseconds.
function rtc_ns(): i64 {
  unsafe {
    return (__mmio_load32(0x09010000 as usize) as i64 & 0xffffffffi64) * 1000000000i64;
  }
}

// The entropy device of the first virtio-mmio transport that has one.
function find_rng(t: fdt.Tree, f: frames.Frames, tables: pagetable.Tables): Option[virtio_rng.Rng] {
  for i in 0..t.nodes.len() {
    for s in t.property_strings(i, "compatible") {
      if (s == "virtio,mmio") {
        let Some(r) = t.reg(i, 0) else {
          continue;
        };
        match (virtio.probe(r.address as usize)) {
          Some(d) => {
            if (d.id == virtio.ID_ENTROPY) {
              return virtio_rng.open(d, f, tables);
            }
          },
          None => {}
        }
      }
    }
  }
  return None;
}

// Tables that hold the kernel mapping: the RAM and the device window.
function kernel_tables(f: frames.Frames, ram: fdt.Region): Option[pagetable.Tables] {
  let Some(tables) = pagetable.new(f) else {
    return None;
  };
  if (!tables.map_normal(ram.address as usize, ram.size as usize) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    return None;
  }
  return Some(tables);
}

// Moves the heap onto heap_frames frames, keeps the frames of the initrd out
// of the pool, and reads the initrd. The device tree is parsed on the heap of
// the reset entry, below the frames.
function boot(heap_frames: usize): Option[Boot] {
  let Ok(t) = fdt.parse(board.device_tree()) else {
    return None;
  };
  let Some(mem) = t.find_name("memory") else {
    return None;
  };
  let Some(ram) = t.reg(mem, 0) else {
    return None;
  };
  let Some(rd) = initrd(t) else {
    return None;
  };
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    return None;
  };
  let kept = f.reserve(rd.start, rd.end - rd.start);
  let Some(heap) = f.alloc_run(heap_frames) else {
    return None;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  let Ok(a) = cpio.parse(copy_range(rd)) else {
    return None;
  };
  let Some(kernel) = kernel_tables(f, ram) else {
    return None;
  };
  kernel.activate();
  return Some(Boot { f: f, ram: ram, a: a, kernel: kernel, rng: find_rng(t, f, kernel) });
}

function load(b: Boot, path: string, argv: string[]): Option[user_process.Process] {
  let Some(image) = b.a.file(path) else {
    print(path + ": not in the initrd");
    return None;
  };
  return load_image(b, path, image, argv);
}

// Loads the program image into tables of its own, with the initrd as its root,
// path as its exe, the wall clock and the entropy device.
function load_image(b: Boot, path: string, image: u8[], argv: string[]): Option[user_process.Process] {
  let Some(tables) = kernel_tables(b.f, b.ram) else {
    print("no kernel mapping");
    return None;
  };
  match (user_process.load(b.f, tables, image, argv, ["HOME=/"])) {
    Ok(p) => {
      let q = p.with_root(b.a).with_exe(path).with_clock(rtc_ns());
      match (b.rng) {
        Some(r) => {
          return Some(q.with_random((n: i32) => r.read(n)));
        },
        None => {
          return Some(q);
        }
      }
    },
    Err(why) => {
      print(path + ": " + why);
      return None;
    }
  }
}

// Runs p to its end, prints each line that it writes, and gives the process
// after it ended.
function run_to_end(path: string, p: user_process.Process): user_process.Process {
  let line: u8[] = [];
  while (true) {
    let (q, stop) = p.run();
    p = q;
    match (stop) {
      Output(fd, bytes) => {
        line = print_lines(path, fd, line, bytes);
      },
      Exited(status) => {
        print(path + " exit " + status.to_string());
        return p;
      },
      Killed(why) => {
        print(path + " killed: " + why);
        return p;
      },
      Interrupted => {},
      Waiting => {}
    }
  }
  return p;
}

// Makes the kernel's own tables active again and gives back the frames of p.
function release(b: Boot, path: string, p: user_process.Process, free: usize): void {
  b.kernel.activate();
  p.release();
  print(path + " gave back its frames " + (b.f.free_count() == free).to_string());
}

// Prints each line that bytes ends, and gives the start of the next line.
function print_lines(path: string, fd: i32, line: u8[], bytes: u8[]): u8[] {
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
  return line;
}
`

const processMainSrc = processKernelSrc + `
// Runs the program at path to its end. Then the kernel prints what the
// program left in /out/result.txt, makes its own tables active again and gives
// back the frames of the process.
function spawn(b: Boot, path: string, argv: string[]): void {
  let free = b.f.free_count();
  let Some(started) = load(b, path, argv) else {
    return;
  };
  let p = run_to_end(path, started);
  if let Some(out) = p.written("/out/result.txt") {
    unsafe {
      print(path + " left /out/result.txt: " + string_from_bytes_unchecked(out));
    }
  }
  release(b, path, p, free);
}

function main(): i32 {
  let Some(b) = boot(4096 as usize) else {
    print("no boot");
    return 1;
  };
  spawn(b, "/bin/hello", ["/bin/hello", "world"]);
  spawn(b, "/bin/efault", ["/bin/efault"]);
  spawn(b, "/bin/crash", ["/bin/crash"]);
  spawn(b, "/bin/files", ["/bin/files"]);
  spawn(b, "/bin/info", ["/bin/info"]);
  spawn(b, "/bin/writer", ["/bin/writer"]);
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

// The /etc of the initrd of every process test.
var etcFiles = []cpioFile{
	{"etc", 0o40755, nil},
	{"etc/motd", 0o100644, []byte("hello from the initrd\n")},
	{"etc/hostname", 0o100644, []byte("fernos\n")},
	{"etc/issue", 0o120777, []byte("motd")},
}

// files reads the root filesystem through the Linux calls: by an absolute
// path, by a relative path with "..", through a symbolic link, and as a
// directory listing. A missing file and a write over the archive both fail.
const processFilesSrc = `import "std/array";
import "std/string";

function show(label: string, r: Result[string, IoError]): void {
  match (r) {
    Ok(s) => {
      print(label + ": " + s.trim());
    },
    Err(e) => {
      print(label + " failed");
    }
  }
}

function main(): i32 {
  show("motd", read_file("/etc/motd"));
  show("hostname", read_file("bin/../etc/hostname"));
  show("issue", read_file("/etc/issue"));
  show("missing", read_file("/etc/missing"));
  match (read_dir("/etc")) {
    Ok(names) => {
      print("etc: " + names.join(","));
    },
    Err(e) => {
      print("etc failed");
    }
  }
  match (write_file("/etc/hostname", "x")) {
    Ok(n) => {
      print("write succeeded");
    },
    Err(e) => {
      print("write failed");
    }
  }
  return 0;
}
`

// info reads the stats of a file and a directory, the image of the program
// through /proc/self/exe, and both clocks.
const processInfoSrc = `import "std/rand";

function main(): i32 {
  let start = monotonic_ns();
  print("wall clock after 2024 " + (now_unix_ms() > 1704067200000i64).to_string());
  match (stat("/etc/motd")) {
    Ok(s) => {
      print("motd size " + s.size.to_string() + ", file " + s.is_file.to_string());
    },
    Err(e) => {
      print("motd stat failed");
    }
  }
  match (stat("/etc")) {
    Ok(s) => {
      print("etc is a directory " + s.is_dir.to_string());
    },
    Err(e) => {
      print("etc stat failed");
    }
  }
  match (read_link_bytes("/proc/self/exe")) {
    Ok(b) => {
      unsafe {
        print("exe " + string_from_bytes_unchecked(b));
      }
    },
    Err(e) => {
      print("exe link failed");
    }
  }
  match (read_file_bytes("/proc/self/exe")) {
    Ok(b) => {
      print("image is ELF " + (b.len() > 4 && b[1] == 69 as u8 && b[2] == 76 as u8).to_string());
    },
    Err(e) => {
      print("image read failed");
    }
  }
  print("monotonic advances " + (monotonic_ns() > start).to_string());
  let a = rand.bytes(32);
  let b = rand.bytes(32);
  let same = true;
  let zero = true;
  for i in 0..32 {
    same = same && a[i] == b[i];
    zero = zero && a[i] == 0 as u8;
  }
  print("random " + a.len().to_string() + " bytes, two reads differ " + (!same).to_string() + ", not zero " + (!zero).to_string());
  return 0;
}
`

// writer makes a directory tree in memory, writes a file in two parts,
// renames it into place, and reads it back. It also writes over the middle of
// a file, sets a mode, lists the new directory, and removes a file. Each change
// of the archive fails with EROFS.
const processWriterSrc = `import "std/array";

function ok(label: string, r: Result[void, IoError]): void {
  match (r) {
    Ok(v) => {
      print(label + " ok");
    },
    Err(e) => {
      print(label + " failed");
    }
  }
}

function main(): i32 {
  ok("mkdir", create_dir_all("/home/user/.cache/fern"));
  ok("write", write_file("/home/user/.cache/fern/tmp-key", "key 1"));
  ok("rename", rename("/home/user/.cache/fern/tmp-key", "/home/user/.cache/fern/key.txt"));
  ok("chmod", chmod("/home/user/.cache/fern/key.txt", 384));
  match (read_file("/home/user/.cache/fern/key.txt")) {
    Ok(s) => {
      print("read back " + s);
    },
    Err(e) => {
      print("read back failed");
    }
  }
  match (stat("/home/user/.cache/fern/key.txt")) {
    Ok(st) => {
      print("mode " + (st.mode & 4095 as u32).to_string() + ", size " + st.size.to_string());
    },
    Err(e) => {
      print("stat failed");
    }
  }
  ok("scratch", write_file("/home/user/scratch", "x"));
  ok("remove", remove_file("/home/user/scratch"));
  match (read_dir("/home/user/.cache/fern")) {
    Ok(names) => {
      print("cache: " + names.join(","));
    },
    Err(e) => {
      print("cache listing failed");
    }
  }
  ok("write over the initrd", write_file("/etc/motd", "no"));
  ok("rename the initrd", rename("/etc/motd", "/etc/gone"));
  ok("mkdir in a missing parent", create_dir_all("/etc/motd/x"));
  ok("dir a", create_dir_all("/tmp/a"));
  ok("dir b", create_dir_all("/tmp/b"));
  ok("remove a", remove_dir("/tmp/a"));
  ok("dir c", create_dir_all("/tmp/c"));
  match (stat("/tmp/b")) {
    Ok(b) => {
      match (stat("/tmp/c")) {
        Ok(c) => {
          print("b and c have their own inodes " + (b.ino != c.ino).to_string());
        },
        Err(e) => {
          print("stat c failed");
        }
      }
    },
    Err(e) => {
      print("stat b failed");
    }
  }
  ok("out", create_dir_all("/out"));
  ok("output", write_file("/out/result.txt", "made on fernos"));
  return 0;
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
	return codeImage(0x400000, []uint32{
		0xd2800020, // movz x0, #1
		0xd2a80001, // movz x1, #0x4000, lsl #16
		0xd28000a2, // movz x2, #5
		0xd2800808, // movz x8, #64
		0xd4000001, // svc #0
		0xcb0003e0, // neg x0, x0
		0xd2800bc8, // movz x8, #94
		0xd4000001, // svc #0
	})
}

// codeImage is a static arm64 ELF of one read and execute segment at base,
// which holds the headers and then code, the entry.
func codeImage(base uint64, code []uint32) []byte {
	const headers = 64 + 56
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

// processInitrd builds each Fern program for arm64-linux, and writes a cpio
// archive with it and each raw image as bin/NAME, and the files of etcFiles.
func processInitrd(t *testing.T, cli *selfHostCLI, dir string, fern map[string]string, raw map[string][]byte) string {
	t.Helper()
	programs := map[string][]byte{}
	for name, b := range raw {
		programs[name] = b
	}
	for name, src := range fern {
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
		programs[name] = b
	}
	names := make([]string, 0, len(programs))
	for name := range programs {
		names = append(names, name)
	}
	sort.Strings(names)
	files := []cpioFile{{"bin", 0o40755, nil}}
	for _, name := range names {
		files = append(files, cpioFile{"bin/" + name, 0o100755, programs[name]})
	}
	files = append(files, etcFiles...)
	archive := filepath.Join(dir, "initrd.cpio")
	if err := os.WriteFile(archive, newcArchive(files), 0o644); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestSelfHostKernelProcessOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrd(t, cli, dir, map[string]string{"hello": processHelloSrc, "crash": processCrashSrc, "files": processFilesSrc, "info": processInfoSrc, "writer": processWriterSrc}, map[string][]byte{"efault": efaultImage()})
	src := writeTickProgram(t, processBoardSrc, processMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "256M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive,
		"-global", "virtio-mmio.force-legacy=false", "-device", "virtio-rng-device")
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
		"/bin/files fd 1: motd: hello from the initrd\n",
		"/bin/files fd 1: hostname: fernos\n",
		"/bin/files fd 1: issue: hello from the initrd\n",
		"/bin/files fd 1: missing failed\n",
		"/bin/files fd 1: etc: motd,hostname,issue\n",
		"/bin/files fd 1: write failed\n",
		"/bin/files exit 0\n",
		"/bin/files gave back its frames true\n",
		"/bin/info fd 1: wall clock after 2024 true\n",
		"/bin/info fd 1: motd size 22, file true\n",
		"/bin/info fd 1: etc is a directory true\n",
		"/bin/info fd 1: exe /bin/info\n",
		"/bin/info fd 1: image is ELF true\n",
		"/bin/info fd 1: monotonic advances true\n",
		"/bin/info fd 1: random 32 bytes, two reads differ true, not zero true\n",
		"/bin/info exit 0\n",
		"/bin/writer fd 1: mkdir ok\n",
		"/bin/writer fd 1: write ok\n",
		"/bin/writer fd 1: rename ok\n",
		"/bin/writer fd 1: chmod ok\n",
		"/bin/writer fd 1: read back key 1\n",
		"/bin/writer fd 1: mode 384, size 5\n",
		"/bin/writer fd 1: scratch ok\n",
		"/bin/writer fd 1: remove ok\n",
		"/bin/writer fd 1: cache: key.txt\n",
		"/bin/writer fd 1: write over the initrd failed\n",
		"/bin/writer fd 1: rename the initrd failed\n",
		"/bin/writer fd 1: mkdir in a missing parent failed\n",
		"/bin/writer fd 1: remove a ok\n",
		"/bin/writer fd 1: b and c have their own inodes true\n",
		"/bin/writer fd 1: out ok\n",
		"/bin/writer fd 1: output ok\n",
		"/bin/writer exit 0\n",
		"/bin/writer left /out/result.txt: made on fernos\n",
	} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
