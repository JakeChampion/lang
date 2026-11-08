package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The qemu-virt board of the maps test.
const dmaBoardSrc = `// The board is qemu-virt with a GICv3: a PL011 UART, the distributor and the
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

// Enables the SPI line id (32 or more) at the distributor: group 1, priority
// 0x80, edge-triggered, routed to this core.
pub function gic_enable(id: i32): void {
  unsafe {
    let word = (id / 32 * 4) as usize;
    let bit = 1 << id % 32;
    __mmio_store32(GICD + 0x80 + word, __mmio_load32(GICD + 0x80 + word) | bit);
    __mmio_store8(GICD + 0x400 + id as usize, 0x80);
    let cfg = GICD + 0xc00 + (id / 16 * 4) as usize;
    __mmio_store32(cfg, __mmio_load32(cfg) | (2 << id % 16 * 2));
    __mmio_store64(GICD + 0x6000 + (id * 8) as usize, 0i64);
    __mmio_store32(GICD + 0x100 + word, bit);
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
// timer tick in tpidr_el0 and rearms the timer, and acknowledges the line.
@interrupt("irq")
function on_irq(): void {
  unsafe {
    let id = __sysreg_read("icc_iar1_el1") as i32;
    if (id < 1020) {
      let word = __irq_pending() + (id / 32 * 4) as usize;
      __mmio_store32(word, __mmio_load32(word) | (1 << (id % 32)));
      if (id == TIMER_IRQ) {
        __sysreg_write("tpidr_el0", __sysreg_read("tpidr_el0") + 1i64);
        __sysreg_write("cntp_tval_el0", timer_period());
      }
      __sysreg_write("icc_eoir1_el1", id as i64);
    }
  }
}

// The ticks that the top half counted.
pub function irq_count(): i64 {
  unsafe {
    return __sysreg_read("tpidr_el0");
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

// The device window of qemu-virt: the GIC, the UART and the other
// peripherals from 0x08000000, and the 32 virtio-mmio transports from
// 0x0a000000, up to 0x0b000000.
pub const DEVICES: usize = 0x08000000;

pub const DEVICES_BYTES: usize = 0x03000000;

// The top half of a synchronous exception: it records the exception class
// and the faulting address in tpidrro_el0 and tpidr_el1, and steps over the
// instruction.
@interrupt("sync")
function on_sync(): void {
  unsafe {
    __sysreg_write("tpidrro_el0", __sysreg_read("esr_el1") >> 26i64);
    __sysreg_write("tpidr_el1", __sysreg_read("far_el1"));
    __sysreg_write("elr_el1", __sysreg_read("elr_el1") + 4i64);
  }
}

// The class of the last synchronous exception, and its faulting address.
pub function fault(): string {
  unsafe {
    return __sysreg_read("tpidrro_el0").to_string() + " far " + __sysreg_read("tpidr_el1").to_string();
  }
}
`

// The DMA region (#14255): on the kernel mapping of the maps test, the image
// takes a std/dma buffer, a run of frames remapped as device memory outside
// the heap, which splits a 2 MiB block of RAM into pages. It writes a pattern
// into the buffer, runs the tick program with its heap churn, finds the
// pattern intact, and returns the frames.
const dmaMainSrc = `import "./board";
import "std/async";
import "std/fdt";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/dma";

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
    __sysreg_write("tpidr_el0", 0i64);
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
  let heap_frames = f.free_count() - 64 as usize;
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
  let mapped = tables.map_normal(r.address as usize, r.size as usize) && tables.map_device(board.DEVICES, board.DEVICES_BYTES);
  print("mapped " + mapped.to_string() + " frames left " + (f.free_count() as i64).to_string());
  tables.activate();
  print("active root is the new root " + (pagetable.active_root() == tables.root).to_string());
  let Some(buf) = dma.alloc(f, tables, 12000 as usize) else {
    print("no dma buffer");
    return 1;
  };
  // A pattern that the device would write, kept through the heap churn below.
  unsafe {
    let w = 0 as usize;
    while (w < buf.bytes) {
      __store_i64(buf.virtual + w, (w as i64) * 7i64 + 1i64);
      w += 8 as usize;
    }
  }
  buf.sync();
  print("dma bytes " + (buf.bytes as i64).to_string() + " physical is virtual " + (buf.physical == buf.virtual).to_string() + " outside the heap " + buf.outside(heap, heap_end).to_string() + " in the frames " + (buf.virtual >= f.base && buf.virtual + buf.bytes <= f.end()).to_string());
  unsafe {
    let stray = __mmio_load32(0x20000000 as usize);
  }
  print("unmapped access fault " + board.fault());
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
  let intact = true;
  unsafe {
    let w = 0 as usize;
    while (w < buf.bytes) {
      if (__load_i64(buf.virtual + w) != (w as i64) * 7i64 + 1i64) {
        intact = false;
      }
      w += 8 as usize;
    }
  }
  let before = f.free_count();
  print("dma intact " + intact.to_string() + " freed " + dma.free(f, tables, buf).to_string() + " frames back " + (f.free_count() == before + 3 as usize).to_string());
  let irqs = board.irq_count();
  print("irqs at least 20 " + (irqs >= 20i64).to_string() + ", " + irqs.to_string());
  return 0;
}
`

// The qemu microvm board of the maps test.
const dmaX86BoardSrc = `// The board is qemu microvm: COM1 at port 0x3f8, the isa-debug-exit device
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

// The vector of the network device, and the IOAPIC that routes its GSI there.
pub const NET_IRQ: i32 = 33;

const IOAPIC: usize = 0xfec00000;

// Routes the IOAPIC input gsi to NET_IRQ on this core, edge-triggered,
// active high and unmasked.
pub function ioapic_route(gsi: i32): void {
  unsafe {
    __mmio_store32(IOAPIC, 0x11 + 2 * gsi);
    __mmio_store32(IOAPIC + 0x10, 0);
    __mmio_store32(IOAPIC, 0x10 + 2 * gsi);
    __mmio_store32(IOAPIC + 0x10, NET_IRQ);
  }
}

// The top half of the network interrupt: it sets the vector's bit in the IRQ
// region and signals the end of interrupt.
@interrupt("33")
function on_net(): void {
  unsafe {
    let word = __irq_pending() + (NET_IRQ / 32 * 4) as usize;
    __mmio_store32(word, __mmio_load32(word) | (1 << (NET_IRQ % 32)));
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

// The device window of qemu microvm above the RAM: the eight virtio-mmio
// transports from 0xfeb00000, then the IOAPIC and the local APIC, up to
// 0xff000000.
pub const VIRTIO: usize = 0xfeb00000;

pub const DEVICES: usize = 0xfeb00000;

pub const DEVICES_BYTES: usize = 0x00500000;

// The command line, up to 256 bytes and its NUL.
pub function command_line(info: pvh.StartInfo): u8[] {
  return copy(info.cmdline_address as usize, 257);
}

// A page fault ends the run: qemu exits with 35 for the value 17. The test
// asks for the fault through the command line, after everything else ran.
@interrupt("pf")
function on_page_fault(): void {
  unsafe {
    __port_out8(0xf4, 17);
  }
}
`

const dmaX86MainSrc = `import "./board";
import "std/async";
import "std/pvh";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/dma";

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

// Whether one RAM region of the memory map holds the frames.
function ram(regions: pvh.MemoryRegion[], start: usize, end: usize): string {
  for r in regions {
    if (r.is_ram() && r.address <= start as u64 && r.address + r.size >= end as u64) {
      return "ram " + r.address.to_string() + " size " + r.size.to_string() + " holds the frames";
    }
  }
  return "no ram region holds the frames";
}

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
  let Ok(info) = pvh.parse(board.start_info()) else {
    print("no start info");
    return 1;
  };
  let Ok(regions) = pvh.parse_memmap(board.memmap(info), info.memmap_entries) else {
    print("no memory map");
    return 1;
  };
  let Ok(line) = pvh.command_line(board.command_line(info)) else {
    print("no command line");
    return 1;
  };
  print(ram(regions, f.base, f.end()));
  print(exhaust(f));
  // The kernel heap moves onto a run of frames.
  let heap_frames = f.free_count() - 64 as usize;
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
  let Some(tables) = pagetable.new(f) else {
    print("no root frame");
    return 1;
  };
  let mapped = map_all(tables, regions);
  print("mapped " + mapped.to_string() + " frames left " + (f.free_count() as i64).to_string());
  tables.activate();
  print("active root is the new root " + (pagetable.active_root() == tables.root).to_string());
  let Some(buf) = dma.alloc(f, tables, 12000 as usize) else {
    print("no dma buffer");
    return 1;
  };
  // A pattern that the device would write, kept through the heap churn below.
  unsafe {
    let w = 0 as usize;
    while (w < buf.bytes) {
      __store_i64(buf.virtual + w, (w as i64) * 7i64 + 1i64);
      w += 8 as usize;
    }
  }
  buf.sync();
  print("dma bytes " + (buf.bytes as i64).to_string() + " physical is virtual " + (buf.physical == buf.virtual).to_string() + " outside the heap " + buf.outside(heap, heap_end).to_string() + " in the frames " + (buf.virtual >= f.base && buf.virtual + buf.bytes <= f.end()).to_string());
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
  let intact = true;
  unsafe {
    let w = 0 as usize;
    while (w < buf.bytes) {
      if (__load_i64(buf.virtual + w) != (w as i64) * 7i64 + 1i64) {
        intact = false;
      }
      w += 8 as usize;
    }
  }
  let before = f.free_count();
  print("dma intact " + intact.to_string() + " freed " + dma.free(f, tables, buf).to_string() + " frames back " + (f.free_count() == before + 3 as usize).to_string());
  let irqs = board.irq_count();
  print("irqs at least 20 " + (irqs >= 20i64).to_string() + ", " + irqs.to_string());
  if (line == "probe") {
    print("unmapped access");
    unsafe {
      let stray = __mmio_load32(0x20000000 as usize);
    }
    print("the page fault did not end the run");
  }
  return 16;
}
`

func TestSelfHostKernelDMAOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, dmaMainSrc)
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
			// Class 37 is a data abort from the current exception level, and
			// 536870912 is the probed address 0x20000000.
			for _, want := range []string{"heap on frames true\n", "mapped true frames left ", "active root is the new root true\n", "unmapped access fault 37 far 536870912\n", "wakes 20 total 320400\n", "irqs at least 20 true, ", "dma bytes 12288 physical is virtual true outside the heap true in the frames true\n", "dma intact true freed true frames back true\n"} {
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

// The plain run asks for the fault probe through the command line, and the
// page-fault handler ends it with exit 35. The leak-check run returns from
// main, prints its census, and exits with 33.
func TestSelfHostKernelX86DMAOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, dmaX86MainSrc)
	for _, c := range []struct {
		name   string
		env    []string
		append string
		exit   int
	}{{"plain", nil, "probe", 35}, {"leakcheck", []string{"FERN_LEAKCHECK=1"}, "fern=pvh", 33}} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			cmd := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM)
			cmd.Env = append(os.Environ(), c.env...)
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			console, err := bootQemu(t, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
				"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
				"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", c.append)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != c.exit {
				t.Fatalf("qemu: %v, want exit status %d from isa-debug-exit; console:\n%s", err, c.exit, console)
			}
			for _, want := range []string{" holds the frames\n", "heap on frames true\n", "mapped true frames left ", "active root is the new root true\n", "wakes 20 total 320440\n", "irqs at least 20 true, ", "dma bytes 12288 physical is virtual true outside the heap true in the frames true\n", "dma intact true freed true frames back true\n"} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			wantFrames(t, console)
			if c.env != nil {
				wantCensus(t, console)
			} else if !strings.Contains(string(console), "unmapped access\n") || strings.Contains(string(console), "did not end the run") {
				t.Errorf("console:\n%s\nwant the probe line and no line after the page fault", console)
			}
		})
	}
}
