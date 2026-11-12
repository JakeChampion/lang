package e2ecompiler

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The RAM of an x86-64 kernel that runs processes. An x86-64-linux program
// links at 0x400000, so the image and its RAM lie above the programs, at
// 1 GiB.
const processX86RAM = "0x40000000,125M"

// The qemu microvm board of the virtio-blk test, with its RAM at 1 GiB, the
// time-stamp counter as the counter of a process, and the CMOS clock. Its
// page-fault handler ends the run, so a fault of a process must not reach it.
var processX86BoardSrc = strings.Replace(blkX86BoardSrc, "pub const RAM_BASE: usize = 0x200000;", "pub const RAM_BASE: usize = 0x40000000;", 1) + `
// The count of channel 0 of the PIT, which goes down at 1193182 Hz.
function pit_count(): i32 {
  unsafe {
    __port_out8(0x43, 0);
    let low = __port_in8(0x40);
    return __port_in8(0x40) << 8 | low;
  }
}

// The frequency of the time-stamp counter: its ticks in 11932 ticks of the
// PIT, which are 10 ms.
pub function tsc_hz(): i64 {
  unsafe {
    __port_out8(0x43, 0x34);
    __port_out8(0x40, 0);
    __port_out8(0x40, 0);
    let start = pit_count();
    let from = __sysreg_read("tsc");
    while ((start - pit_count() & 0xffff) < 11932) {}
    return (__sysreg_read("tsc") - from) * 100i64;
  }
}

// The time of the time-stamp counter in nanoseconds, at the frequency hz.
pub function counter_ns(hz: i64): i64 {
  unsafe {
    let c = __sysreg_read("tsc");
    return c / hz * 1000000000i64 + c % hz * 1000000000i64 / hz;
  }
}

// The Unix time of the start of the year of the CMOS real-time clock, in
// nanoseconds. Register 9 holds the year of the century in BCD.
pub function rtc_ns(): i64 {
  unsafe {
    __port_out8(0x70, 9);
    let bcd = __port_in8(0x71);
    let year = 2000 + (bcd >> 4) * 10 + (bcd & 15);
    return (year - 1970) as i64 * 31557600i64 * 1000000000i64;
  }
}
`

// processX86KernelSrc is processKernelSrc for qemu microvm: the start info of
// PVH gives the initrd, and the virtio-mmio transports are at fixed addresses.
const processX86KernelSrc = `import "./board";
import "std/cpio";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/pvh";
import "std/user_process";
import "std/virtio";
import "std/virtio_rng";

struct Boot { f: frames.Frames, a: cpio.Archive, kernel: pagetable.Tables, rng: Option[virtio_rng.Rng], hz: i64 }

// The entropy device of the first virtio-mmio transport that has one.
function find_rng(f: frames.Frames, tables: pagetable.Tables): Option[virtio_rng.Rng] {
  for i in 0..8 {
    match (virtio.probe(board.VIRTIO + i as usize * 512 as usize)) {
      Some(d) => {
        if (d.id == virtio.ID_ENTROPY) {
          return virtio_rng.open(d, f, tables);
        }
      },
      None => {}
    }
  }
  return None;
}

// Tables that hold the kernel mapping: the RAM of the image and the device
// window. The low memory stays free for the processes.
function kernel_tables(f: frames.Frames): Option[pagetable.Tables] {
  let Some(tables) = pagetable.new(f) else {
    return None;
  };
  if (!tables.map_normal(board.RAM_BASE, board.RAM_SIZE) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    return None;
  }
  return Some(tables);
}

// Moves the heap onto heap_frames frames and reads the initrd, which the VMM
// put above the RAM of the image. The start info is parsed on the heap of
// the reset entry, below the frames.
function boot(heap_frames: usize): Option[Boot] {
  let Ok(info) = pvh.parse(board.start_info()) else {
    return None;
  };
  let Ok(mods) = pvh.parse_modules(board.modules(info), info.modules) else {
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
  let Some(heap) = f.alloc_run(heap_frames) else {
    return None;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  if (mods.len() == 0) {
    return None;
  }
  let Ok(a) = cpio.parse(board.module_bytes(mods[0])) else {
    return None;
  };
  let Some(kernel) = kernel_tables(f) else {
    return None;
  };
  kernel.activate();
  return Some(Boot { f: f, a: a, kernel: kernel, rng: find_rng(f, kernel), hz: board.tsc_hz() });
}

function load(b: Boot, path: string, argv: string[]): Option[user_process.Process] {
  let Some(image) = b.a.file(path) else {
    print(path + ": not in the initrd");
    return None;
  };
  return load_image(b, path, image, argv);
}

// Loads the program image into tables of its own, with the initrd as its root,
// path as its exe, the time-stamp counter, the wall clock and the entropy
// device.
function load_image(b: Boot, path: string, image: u8[], argv: string[]): Option[user_process.Process] {
  let Some(tables) = kernel_tables(b.f) else {
    print("no kernel mapping");
    return None;
  };
  let hz = b.hz;
  match (user_process.load(b.f, tables, image, argv, ["HOME=/"])) {
    Ok(p) => {
      let q = p.with_root(b.a).with_exe(path).with_counter(() => board.counter_ns(hz)).with_clock(board.rtc_ns());
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

// On x86-64, main returns and the exit provider stops the VM.
var processX86MainSrc = processX86KernelSrc + strings.Replace(processSpawnSrc, "  board.power_off(0);\n", "", 1)

var schedX86MainSrc = processX86KernelSrc + strings.Replace(strings.Replace(schedLoopSrc, "  board.power_off(0);\n", "", 1), "  board.gic_init();\n  board.timer_start();\n", "  board.apic_init();\n", 1)

// efaultX86Image is efaultImage for x86-64: a write from a kernel address,
// then exit_group with the negated result.
func efaultX86Image() []byte {
	return elfImage(62, 0x400000, []byte{
		0xbf, 1, 0, 0, 0, // mov $1, %edi
		0xbe, 0, 0, 0, 0x40, // mov $0x40000000, %esi
		0xba, 5, 0, 0, 0, // mov $5, %edx
		0xb8, 1, 0, 0, 0, // mov $1, %eax
		0x0f, 0x05, // syscall
		0x48, 0xf7, 0xd8, // neg %rax
		0x89, 0xc7, // mov %eax, %edi
		0xb8, 231, 0, 0, 0, // mov $231, %eax
		0x0f, 0x05, // syscall
	})
}

// peekX86Image lies at 0x800000 and loads from 0x400000, where the worker has
// its code. That address is not in the tables of peek, so the load faults.
func peekX86Image() []byte {
	return elfImage(62, 0x800000, []byte{
		0x48, 0x8b, 0x04, 0x25, 0, 0, 0x40, 0, // mov 0x400000, %rax
		0x31, 0xff, // xor %edi, %edi
		0xb8, 231, 0, 0, 0, // mov $231, %eax
		0x0f, 0x05, // syscall
	})
}

// norandomX86Image calls getrandom into its stack and exits with the negated
// result, which is ENOSYS for a process with no source.
func norandomX86Image() []byte {
	return elfImage(62, 0x400000, []byte{
		0x48, 0x8d, 0x7c, 0x24, 0xc0, // lea -64(%rsp), %rdi
		0xbe, 16, 0, 0, 0, // mov $16, %esi
		0x31, 0xd2, // xor %edx, %edx
		0xb8, 0x3e, 1, 0, 0, // mov $318, %eax
		0x0f, 0x05, // syscall
		0x48, 0xf7, 0xd8, // neg %rax
		0x89, 0xc7, // mov %eax, %edi
		0xb8, 231, 0, 0, 0, // mov $231, %eax
		0x0f, 0x05, // syscall
	})
}

// bootProcessX86 builds the kernel and boots it on qemu microvm with the
// initrd. main returns 0, so qemu exits with 1 from isa-debug-exit.
func bootProcessX86(t *testing.T, cli *selfHostCLI, dir, main, archive string, devices ...string) string {
	t.Helper()
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	kernel := filepath.Join(dir, "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", kernel, writeTickProgram(t, processX86BoardSrc, main), "-ram", processX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	args := []string{"-M", "microvm,acpi=off", "-cpu", "max", "-m", "1280M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", kernel, "-append", "fern=pvh", "-initrd", archive}
	console, err := bootQemu(t, qemu, append(args, devices...)...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("qemu: %v, want exit status 1 from isa-debug-exit; console:\n%s", err, console)
	}
	return string(console)
}

// Milestone 4 of docs/BARE-METAL-PLAN.md (#14263) on x86-64: the kernel loads
// static x86-64-linux programs from the initrd, each in its own page tables,
// and runs them in ring 3. The programs are those of the arm64 test.
func TestSelfHostKernelX86ProcessOnQemu(t *testing.T) {
	e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrdFor(t, cli, "x86-64-linux", dir, map[string]string{"hello": processHelloSrc, "crash": processCrashSrc, "files": processFilesSrc, "info": processInfoSrc, "writer": processWriterSrc}, map[string][]byte{"efault": efaultX86Image()})
	console := bootProcessX86(t, cli, dir, processX86MainSrc, archive, "-global", "virtio-mmio.force-legacy=false", "-device", "virtio-rng-device")
	// The page fault from ring 3 has the vector 14, and CR2 holds the kernel
	// address. The kernel's own page-fault handler does not run.
	for _, want := range []string{
		"/bin/hello fd 1: hello world from EL0\n",
		"/bin/hello fd 1: parts 2000, last 1999, depth 20000\n",
		"/bin/hello exit 3\n",
		"/bin/efault exit 14\n",
		"/bin/crash fd 1: crash loads a kernel address\n",
		fmt.Sprintf("/bin/crash killed: a fault of class 14 at %d from ", 0x40000000),
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
		"/bin/writer fd 1: read back key 1\n",
		"/bin/writer fd 1: mode 384, size 5\n",
		"/bin/writer fd 1: cache: key.txt\n",
		"/bin/writer fd 1: write over the initrd failed\n",
		"/bin/writer fd 1: b and c have their own inodes true\n",
		"/bin/writer exit 0\n",
		"/bin/writer left /out/result.txt: made on fernos\n",
		"/bin/writer gave back its frames true\n",
	} {
		if !strings.Contains(console, want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// The three "done when" items of #14263 on x86-64. Two workers with separate
// heaps run under the local APIC timer. A tick in ring 3 runs its top half in
// ring 0 and returns to the kernel loop, which runs the next process.
func TestSelfHostKernelX86SchedulerOnQemu(t *testing.T) {
	e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrdFor(t, cli, "x86-64-linux", dir, map[string]string{"worker": schedWorkerSrc}, map[string][]byte{"efault": efaultX86Image(), "peek": peekX86Image(), "norandom": norandomX86Image()})
	console := bootProcessX86(t, cli, dir, schedX86MainSrc, archive)
	for _, want := range []string{
		"/bin/worker a fd 1: a total 207000\n",
		"/bin/worker b fd 1: b total 207000\n",
		"/bin/worker a exit 0\n",
		"/bin/worker b exit 0\n",
		"/bin/efault exit 14\n",
		"/bin/peek killed: a fault of class 14 at 4194304 from ",
		"/bin/worker a preempted true\n",
		"/bin/worker b preempted true\n",
		"/bin/norandom exit 38\n",
		"ticks true, frames back true\n",
	} {
		if !strings.Contains(console, want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
	for _, early := range []string{"/bin/efault exit 14\n", "/bin/peek killed"} {
		for _, late := range []string{"a total", "b total"} {
			if i, j := strings.Index(console, early), strings.Index(console, late); i < 0 || j < 0 || i > j {
				t.Errorf("console:\n%s\nwant %q before %q", console, early, late)
			}
		}
	}
}
