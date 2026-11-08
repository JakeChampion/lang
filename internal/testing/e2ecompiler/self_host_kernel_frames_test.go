package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The qemu-virt board of the tick test, plus the RAM that -ram gives the
// image and the device tree that names the RAM.
const framesBoardSrc = `// The board is qemu-virt with a GICv3: a PL011 UART, the distributor and the
// first redistributor at their fixed addresses, and the EL1 physical timer on
// PPI 14, which is IRQ line 30.
const UART: usize = 0x09000000;

const GICD: usize = 0x08000000;

const GICR: usize = 0x080A0000;

pub const TIMER_IRQ: i32 = 30;

@provide("log")
pub function uart_write(data: usize, len: i32): void {
  unsafe {
    let i = 0;
    while (i < len) {
      __mmio_store8(UART, __load_u8(data + i as usize));
      i += 1;
    }
  }
}

// PSCI SYSTEM_OFF.
@provide("exit")
pub function power_off(code: i32): void {
  unsafe {
    __hvc(0x84000008i64, 0i64, 0i64, 0i64);
  }
}

// Enables group 1 interrupts at the distributor, wakes the redistributor of
// this core, enables the timer line at priority 0x80, and opens the CPU
// interface to every priority.
pub function gic_init(): void {
  unsafe {
    __mmio_store32(GICD, 0x12);
    let waker = GICR + 0x14;
    __mmio_store32(waker, __mmio_load32(waker) & -3);
    while ((__mmio_load32(waker) & 4) != 0) {}
    let sgi = GICR + 0x10000;
    __mmio_store32(sgi + 0x80, -1);
    __mmio_store8(sgi + 0x400 + TIMER_IRQ as usize, 0x80);
    __mmio_store32(sgi + 0x100, 1 << TIMER_IRQ);
    __sysreg_write("icc_sre_el1", __sysreg_read("icc_sre_el1") | 1i64);
    __sysreg_write("icc_pmr_el1", 0xffi64);
    __sysreg_write("icc_igrpen1_el1", 1i64);
  }
}

// The timer period: one millisecond of the counter.
@norc
pub function timer_period(): i64 {
  unsafe {
    return __sysreg_read("cntfrq_el0") / 1000i64;
  }
}

pub function timer_start(): void {
  unsafe {
    __sysreg_write("cntp_tval_el0", timer_period());
    __sysreg_write("cntp_ctl_el0", 1i64);
  }
}

// The top half of every IRQ: it sets the line's bit in the IRQ region, counts a
// timer tick in tpidr_el1 and rearms the timer, and acknowledges the line.
@interrupt("irq")
function on_irq(): void {
  unsafe {
    let id = __sysreg_read("icc_iar1_el1") as i32;
    if (id < 1020) {
      let word = __irq_pending() + (id / 32 * 4) as usize;
      __mmio_store32(word, __mmio_load32(word) | (1 << (id % 32)));
      if (id == TIMER_IRQ) {
        __sysreg_write("tpidr_el1", __sysreg_read("tpidr_el1") + 1i64);
        __sysreg_write("cntp_tval_el0", timer_period());
      }
      __sysreg_write("icc_eoir1_el1", id as i64);
    }
  }
}

// The ticks that the top half counted.
pub function irq_count(): i64 {
  unsafe {
    return __sysreg_read("tpidr_el1");
  }
}

// The RAM that -ram gives the image, and the stack that the reset entry
// keeps at its top. The heap region of the reset entry ends at the stack.
pub const RAM_BASE: usize = 0x40200000;

pub const RAM_SIZE: usize = 132120576;

pub const STACK_BYTES: usize = 1048576;

// The device tree, which qemu-virt puts at the start of its RAM.
const DEVICE_TREE: usize = 0x40000000;

pub function device_tree(): u8[] {
  let out: u8[] = [];
  unsafe {
    let size = 0;
    for k in 4..8 {
      size = size << 8 | __load_u8(DEVICE_TREE + k as usize);
    }
    for k in 0..size {
      out = out.append(__load_u8(DEVICE_TREE + k as usize) as u8);
    }
  }
  return out;
}
`

// Milestone 3, first slice (#14262): the image reads its RAM from the device
// tree, builds std/frames over the RAM above the bootstrap heap's cursor,
// takes and returns every frame, moves the heap onto a run of frames with
// __heap_adopt, and runs the tick program there.
const framesMainSrc = `import "./board";
import "std/async";
import "std/fdt";
import "std/frames";
import "std/option";

// Allocates and frees arrays and strings for a while, longer than a timer
// period, so ticks land in the middle of reference-count operations.
function churn(round: i32): i32 {
  let total = 0;
  let i = 0;
  while (i < 200) {
    let xs: i32[] = [];
    let k = 0;
    while (k < 64) {
      xs = xs.append(k * round);
      k += 1;
    }
    let s = "round " + round.to_string() + " step " + i.to_string();
    total += xs.len() + s.len();
    i += 1;
  }
  return total;
}

// The RAM of the board, from the memory node of the device tree.
function ram(): string {
  let Ok(t) = fdt.parse(board.device_tree()) else {
    return "no device tree";
  };
  let Some(mem) = t.find_name("memory") else {
    return "no memory node";
  };
  let Some(r) = t.reg(mem, 0) else {
    return "no memory reg";
  };
  return "ram " + r.address.to_string() + " size " + r.size.to_string();
}

// Takes every free frame, and gives every one back.
function exhaust(f: frames.Frames): string {
  let before = f.free_count();
  let first = 0 as usize;
  let taken = 0 as usize;
  while (true) {
    let Some(frame) = f.alloc() else {
      break;
    };
    if (taken == 0 as usize) {
      first = frame;
    }
    taken += 1 as usize;
  }
  let k = 0 as usize;
  while (k < taken) {
    let freed = f.free(first + k * frames.FRAME_BYTES);
    k += 1 as usize;
  }
  return "frames " + (f.count() as i64).to_string() + " taken " + (taken as i64).to_string() + " all " + (taken == before).to_string() + " returned " + (f.free_count() == before).to_string();
}

function main(): i32 {
  unsafe {
    __sysreg_write("tpidr_el1", 0i64);
  }
  print(ram());
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
  print(exhaust(f));
  // The kernel heap moves onto a run of frames.
  let heap_frames = f.free_count() - 16 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  let heap_end = heap + heap_frames * frames.FRAME_BYTES;
  let probe = 0 as usize;
  unsafe {
    __heap_adopt(heap as i64, heap_end as i64);
    probe = __alloc(64);
  }
  print("heap on frames " + (probe >= heap && probe < heap_end).to_string());
  unsafe {
    __free(probe, 64);
  }
  board.gic_init();
  board.timer_start();
  __irq_unmask();
  let ticks = async.spawn((): i32 => {
    let n = 0;
    let total = 0;
    while (n < 20) {
      let Some(line) = async.wait(async.irq(board.TIMER_IRQ)) else {
        return -1;
      };
      total += churn(n) + line;
      n += 1;
    }
    return total;
  });
  match (async.block_on(ticks)) {
    Some(total) => {
      print("wakes 20 total " + total.to_string());
    },
    None => {
      print("cancelled");
    }
  }
  let irqs = board.irq_count();
  print("irqs at least 20 " + (irqs >= 20i64).to_string() + ", " + irqs.to_string());
  return 0;
}
`

// The qemu microvm board of the tick test, plus the RAM and the PVH start
// info that names the RAM.
const framesX86BoardSrc = `// The board is qemu microvm: COM1 at port 0x3f8, the isa-debug-exit device
// that the test adds at port 0xf4, and the local APIC timer on vector 32,
// through the xAPIC registers at their fixed address.
import "std/pvh";

const COM1: i32 = 0x3f8;

const LAPIC: usize = 0xfee00000;

pub const TIMER_IRQ: i32 = 32;

@provide("log")
pub function uart_write(data: usize, len: i32): void {
  unsafe {
    let i = 0;
    while (i < len) {
      __port_out8(COM1, __load_u8(data + i as usize));
      i += 1;
    }
  }
}

// qemu exits with the status value * 2 + 1.
@provide("exit")
pub function exit_qemu(code: i32): void {
  unsafe {
    __port_out8(0xf4, code);
  }
}

// Masks both legacy PICs, enables the local APIC, and starts its timer in
// periodic mode on the timer vector: the bus clock divided by 16, 62500 counts
// for each period.
pub function apic_init(): void {
  unsafe {
    __port_out8(0x21, 0xff);
    __port_out8(0xa1, 0xff);
    __mmio_store32(LAPIC + 0xf0, 0x1ff);
    __mmio_store32(LAPIC + 0x3e0, 3);
    __mmio_store32(LAPIC + 0x320, 0x20000 | TIMER_IRQ);
    __mmio_store32(LAPIC + 0x380, 62500);
  }
}

// The top half of the timer interrupt: it sets the vector's bit in the IRQ
// region, counts the tick in kernel_gs_base, and signals the end of interrupt.
@interrupt("32")
function on_timer(): void {
  unsafe {
    let word = __irq_pending() + (TIMER_IRQ / 32 * 4) as usize;
    __mmio_store32(word, __mmio_load32(word) | (1 << (TIMER_IRQ % 32)));
    __sysreg_write("kernel_gs_base", __sysreg_read("kernel_gs_base") + 1i64);
    __mmio_store32(LAPIC + 0xb0, 0);
  }
}

// The ticks that the top half counted.
pub function irq_count(): i64 {
  unsafe {
    return __sysreg_read("kernel_gs_base");
  }
}

// The RAM that -ram gives the image, and the stack that the reset entry
// keeps at its top. The heap region of the reset entry ends at the stack.
pub const RAM_BASE: usize = 0x200000;

pub const RAM_SIZE: usize = 131072000;

pub const STACK_BYTES: usize = 1048576;

function copy(at: usize, n: i32): u8[] {
  let out: u8[] = [];
  unsafe {
    for k in 0..n {
      out = out.append(__load_u8(at + k as usize) as u8);
    }
  }
  return out;
}

// The PVH start info, at the address that the VMM gave in %ebx.
pub function start_info(): u8[] {
  return copy(__boot_info(), pvh.START_INFO_BYTES);
}

pub function memmap(info: pvh.StartInfo): u8[] {
  return copy(info.memmap_address as usize, info.memmap_entries as i32 * pvh.MEMMAP_ENTRY_BYTES);
}
`

const framesX86MainSrc = `import "./board";
import "std/async";
import "std/pvh";
import "std/frames";
import "std/option";

// Allocates and frees arrays and strings for a while, longer than a timer
// period, so ticks land in the middle of reference-count operations.
function churn(round: i32): i32 {
  let total = 0;
  let i = 0;
  while (i < 200) {
    let xs: i32[] = [];
    let k = 0;
    while (k < 64) {
      xs = xs.append(k * round);
      k += 1;
    }
    let s = "round " + round.to_string() + " step " + i.to_string();
    total += xs.len() + s.len();
    i += 1;
  }
  return total;
}

// Whether one RAM region of the PVH memory map holds the frames.
function ram(start: usize, end: usize): string {
  let Ok(info) = pvh.parse(board.start_info()) else {
    return "no start info";
  };
  let Ok(regions) = pvh.parse_memmap(board.memmap(info), info.memmap_entries) else {
    return "no memory map";
  };
  for r in regions {
    if (r.is_ram() && r.address <= start as u64 && r.address + r.size >= end as u64) {
      return "ram " + r.address.to_string() + " size " + r.size.to_string() + " holds the frames";
    }
  }
  return "no ram region holds the frames";
}

// Takes every free frame, and gives every one back.
function exhaust(f: frames.Frames): string {
  let before = f.free_count();
  let first = 0 as usize;
  let taken = 0 as usize;
  while (true) {
    let Some(frame) = f.alloc() else {
      break;
    };
    if (taken == 0 as usize) {
      first = frame;
    }
    taken += 1 as usize;
  }
  let k = 0 as usize;
  while (k < taken) {
    let freed = f.free(first + k * frames.FRAME_BYTES);
    k += 1 as usize;
  }
  return "frames " + (f.count() as i64).to_string() + " taken " + (taken as i64).to_string() + " all " + (taken == before).to_string() + " returned " + (f.free_count() == before).to_string();
}

function main(): i32 {
  unsafe {
    __sysreg_write("kernel_gs_base", 0i64);
  }
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
  print(ram(f.base, f.end()));
  print(exhaust(f));
  // The kernel heap moves onto a run of frames.
  let heap_frames = f.free_count() - 16 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  let heap_end = heap + heap_frames * frames.FRAME_BYTES;
  let probe = 0 as usize;
  unsafe {
    __heap_adopt(heap as i64, heap_end as i64);
    probe = __alloc(64);
  }
  print("heap on frames " + (probe >= heap && probe < heap_end).to_string());
  unsafe {
    __free(probe, 64);
  }
  board.apic_init();
  __irq_unmask();
  let ticks = async.spawn((): i32 => {
    let n = 0;
    let total = 0;
    while (n < 20) {
      let Some(line) = async.wait(async.irq(board.TIMER_IRQ)) else {
        return -1;
      };
      total += churn(n) + line;
      n += 1;
    }
    return total;
  });
  match (async.block_on(ticks)) {
    Some(total) => {
      print("wakes 20 total " + total.to_string());
    },
    None => {
      print("cancelled");
    }
  }
  let irqs = board.irq_count();
  print("irqs at least 20 " + (irqs >= 20i64).to_string() + ", " + irqs.to_string());
  return 16;
}
`

var framesLine = regexp.MustCompile(`frames (\d+) taken (\d+) all true returned true\n`)

// wantFrames checks the frame census: every frame but the one that holds the
// header and the bitmap came out, and every one went back.
func wantFrames(t *testing.T, console []byte) {
	t.Helper()
	m := framesLine.FindSubmatch(console)
	if m == nil {
		t.Errorf("console:\n%s\nwant the frames line", console)
		return
	}
	count, taken := string(m[1]), string(m[2])
	if count == "0" || taken == "0" {
		t.Errorf("frames line %q: no frames", m[0])
	}
	var n, k int
	for _, c := range count {
		n = n*10 + int(c-'0')
	}
	for _, c := range taken {
		k = k*10 + int(c-'0')
	}
	if k != n-1 || n < 1000 {
		t.Errorf("frames line %q: want one reserved frame of a region of at least 1000 frames", m[0])
	}
}

func TestSelfHostKernelFramesOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, framesBoardSrc, framesMainSrc)
	for _, c := range []struct {
		name string
		env  []string
	}{{"plain", nil}, {"leakcheck", []string{"FERN_LEAKCHECK=1"}}} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			cmd := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM)
			cmd.Env = append(os.Environ(), c.env...)
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			console, err := bootQemu(t, qemu, qemuVirtArgs(out)...)
			if err != nil {
				t.Fatalf("qemu: %v; console:\n%s", err, console)
			}
			for _, want := range []string{"ram 1073741824 size 134217728\n", "heap on frames true\n", "wakes 20 total 320400\n", "irqs at least 20 true, "} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			wantFrames(t, console)
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
}

func TestSelfHostKernelX86FramesOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, framesX86BoardSrc, framesX86MainSrc)
	for _, c := range []struct {
		name string
		env  []string
	}{{"plain", nil}, {"leakcheck", []string{"FERN_LEAKCHECK=1"}}} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			cmd := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM)
			cmd.Env = append(os.Environ(), c.env...)
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			console, err := bootQemu(t, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
				"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
				"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 33 {
				t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
			}
			for _, want := range []string{" holds the frames\n", "heap on frames true\n", "wakes 20 total 320440\n", "irqs at least 20 true, "} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			wantFrames(t, console)
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
}
