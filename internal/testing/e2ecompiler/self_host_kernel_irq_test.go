package e2ecompiler

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const irqSrc = `function main(): i32 {
  __irq_mask();
  __irq_wait();
  __irq_unmask();
  unsafe {
    return __mmio_load32(__irq_pending() + 4 as usize);
  }
}
`

// The IRQ ops mask and unmask interrupts, wait for one with interrupts masked,
// and name the IRQ region that each reset entry reserves. Only a kernel target
// grants them.
func TestSelfHostKernelIRQOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range []struct {
		target, ram string
		wants       []string
	}{
		{"arm64-kernel", kernelRAM, []string{
			"    msr daifset, #2\n",
			"    wfi\n    msr daifclr, #2\n    msr daifset, #2\n",
			"    msr daifclr, #2\n",
			"    adrp x0, __fern_irq_pending\n    add x0, x0, :lo12:__fern_irq_pending\n",
		}},
		{"x86-64-kernel", kernelX86RAM, []string{
			"    cli\n",
			"    sti\n    hlt\n    cli\n",
			"    sti\n",
			"    leaq __fern_irq_pending(%rip), %rax\n",
		}},
	} {
		text := emitKernelAsm(t, cli, c.target, c.ram, irqSrc)
		wantInOrder(t, c.target, text, c.wants)
		if !strings.Contains(text, "__fern_irq_pending:\n    .skip 128\n") {
			t.Errorf("%s: the reset entry reserves no IRQ region", c.target)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(irqSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	image, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for name, word := range map[string][]byte{
		"msr daifset, #2": {0xdf, 0x42, 0x03, 0xd5},
		"msr daifclr, #2": {0xff, 0x42, 0x03, 0xd5},
	} {
		if !strings.Contains(string(image), string(word)) {
			t.Errorf("the image has no %s (% x)", name, word)
		}
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		msg, err := cli.compileCmd(target, filepath.Join(dir, "hosted-"+target), src).CombinedOutput()
		for _, op := range []string{"__irq_mask", "__irq_wait", "__irq_unmask", "__irq_pending"} {
			if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), op) {
				t.Errorf("%s: err %v; want E066 naming %s in:\n%s", target, err, op, msg)
			}
		}
	}
	if got := goCheckerCodes(t, dir, irqSrc); len(got) != 0 {
		t.Errorf("Go checker codes = %v, want none", got)
	}
}

// A std/async wait for an interrupt needs the IRQ region, so a hosted target
// refuses the future at compile time.
func TestSelfHostAsyncIRQNeedsAKernel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	const program = `import "std/async";
import "std/option";

function main(): i32 {
  let Some(line) = async.block_on(async.irq(30)) else {
    return 1;
  };
  return line;
}
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("x86-64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "`__irq_pending` (reached via \"irq\" from module \"async\")") {
		t.Errorf("x86-64-linux: err %v; want E066 naming __irq_pending through irq in:\n%s", err, msg)
	}
}

// The board is qemu-virt with a GICv3. The top half of the timer interrupt
// sets the line's bit in the IRQ region, and a std/async task takes each wake
// and churns the heap for longer than a timer period, so ticks land in the
// middle of reference-count operations. A tick that lands while the task runs
// joins the pending bit, so the top half counts at least as many interrupts as
// the task counts wakes.
const tickBoardSrc = `// The board is qemu-virt with a GICv3: a PL011 UART, the distributor and the
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
`

const tickMainSrc = `import "./board";
import "std/async";
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

function main(): i32 {
  unsafe {
    __sysreg_write("tpidr_el1", 0i64);
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

// A wait with a deadline stops a kernel program, because a kernel has no
// clock. exit then reaches the board's exit provider.
const tickDeadlineSrc = `import "./board";
import "std/async";
import "std/option";
import "std/time";

function main(): i32 {
  board.gic_init();
  let none: i32[] = [];
  match (async.wait_any(none, Some(Instant { sec: 1i64, nsec: 0 }))) {
    TimedOut => {
      print("timed out");
    },
    _ => {
      print("woke");
    }
  }
  return 0;
}
`

// The board is qemu microvm with the local APIC timer on vector 32.
const tickX86BoardSrc = `// The board is qemu microvm: COM1 at port 0x3f8, the isa-debug-exit device
// that the test adds at port 0xf4, and the local APIC timer on vector 32,
// through the xAPIC registers at their fixed address.
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

// Enables the local APIC, and starts its timer in
// periodic mode on the timer vector: the bus clock divided by 16, 62500 counts
// for each period.
pub function apic_init(): void {
  unsafe {
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
`

const tickX86MainSrc = `import "./board";
import "std/async";
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

function main(): i32 {
  unsafe {
    __sysreg_write("kernel_gs_base", 0i64);
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

var censusLine = regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n`)

// wantCensus checks the leak census that a FERN_LEAKCHECK image prints after
// main: every allocation freed, and no live bytes.
func wantCensus(t *testing.T, console []byte) {
	t.Helper()
	m := censusLine.FindSubmatch(console)
	if m == nil {
		t.Errorf("console:\n%s\nwant a leakcheck census line", console)
		return
	}
	if string(m[1]) != string(m[2]) || string(m[3]) != "0" {
		t.Errorf("census %s: allocs and frees differ, or live bytes remain", m[0])
	}
	if string(m[1]) == "0" {
		t.Errorf("census %s: the program allocated nothing", m[0])
	}
}

func writeTickProgram(t *testing.T, board, main string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{"board.fern": board, "main.fern": main} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.fern")
}

// bootQemu runs the VM for at most 90 seconds and returns its console and its
// exit error, or fails the test when the VM does not stop.
func bootQemu(t *testing.T, qemu string, args ...string) ([]byte, error) {
	t.Helper()
	return bootQemuFor(t, 90*time.Second, qemu, args...)
}

func bootQemuFor(t *testing.T, limit time.Duration, qemu string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	console, err := exec.CommandContext(ctx, qemu, args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not stop in %v; console:\n%s", limit, console)
	}
	return console, err
}

func qemuVirtArgs(kernel string) []string {
	return []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel}
}

// Milestone 2 on arm64 (#14261): the generic timer ticks through the GICv3
// into a @interrupt top half, and a std/async task counts the wakes while the
// main context allocates and frees. The second image carries the leak census,
// which the reset entry prints after main through the log provider.
func TestSelfHostKernelTicksOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, tickBoardSrc, tickMainSrc)
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
			for _, want := range []string{"wakes 20 total 320400\n", "irqs at least 20 true, "} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
	t.Run("deadline", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "kernel.elf")
		if msg, err := cli.compileCmd("arm64-kernel", out, writeTickProgram(t, tickBoardSrc, tickDeadlineSrc), "-ram", kernelRAM).CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, msg)
		}
		console, err := bootQemu(t, qemu, qemuVirtArgs(out)...)
		if err != nil {
			t.Fatalf("qemu: %v; console:\n%s", err, console)
		}
		if !strings.Contains(string(console), "std/async: a kernel has no clock, so a wait cannot have a deadline\n") || strings.Contains(string(console), "timed out") {
			t.Errorf("console:\n%s\nwant the deadline refusal and no wait", console)
		}
	})
}

// Milestone 2 on x86-64 (#14261): the local APIC timer ticks into the
// @interrupt("32") top half. main returns 16, so the exit provider makes qemu
// exit with 33.
func TestSelfHostKernelX86TicksOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, tickX86BoardSrc, tickX86MainSrc)
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
			for _, want := range []string{"wakes 20 total 320440\n", "irqs at least 20 true, "} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
}
