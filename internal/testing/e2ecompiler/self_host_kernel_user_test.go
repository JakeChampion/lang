package e2ecompiler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Milestone 4 of docs/BARE-METAL-PLAN.md (#14263), its first slice on arm64:
// __user_run enters EL0 with the state of a trap frame, and each exception
// from EL0 saves that state back and returns to the kernel with the kind of
// the trap. The user code makes a write and an exit_group svc, which the
// kernel answers through the frame, and then loads a kernel address, which
// is kernel-only memory and faults.
const userModeMainSrc = `import "./board";
import "std/fdt";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";

// The user window: the process's code at UVA and its stack page above it.
const UVA: usize = 0x1000000000;

const STACK_VA: usize = 0x1000010000;

// x0 = 7; x8 = 64; svc; x0 += 1; x8 = 93; svc; load from x2; spin.
function code(): u32[] {
  return [0xd28000e0u32, 0xd2800808u32, 0xd4000001u32, 0x91000400u32, 0xd2800ba8u32, 0xd4000001u32, 0xf9400041u32, 0x14000000u32];
}

function reg(frame: usize, n: i32): i64 {
  return raw.load_u64(frame + (8 * n) as usize) as i64;
}

function set_reg(frame: usize, n: i32, v: i64): void {
  raw.store_u64(frame + (8 * n) as usize, v as u64);
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
    return 1;
  };
  let Some(r) = t.reg(mem, 0) else {
    return 1;
  };
  let Some(tables) = pagetable.new(f) else {
    return 1;
  };
  let Some(text) = f.alloc() else {
    return 1;
  };
  let Some(stack) = f.alloc() else {
    return 1;
  };
  let Some(frame) = f.alloc() else {
    return 1;
  };
  let words = code();
  for i in 0..words.len() {
    raw.store_u32(text + (4 * i) as usize, words[i]);
  }
  let i = 0 as usize;
  while (i < frames.FRAME_BYTES) {
    raw.store_u64(frame + i, 0 as u64);
    i += 8 as usize;
  }
  if (!tables.map_normal(r.address as usize, r.size as usize) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES) || !tables.map_user(UVA, text, pagetable.PAGE_BYTES, false, true) || !tables.map_user(STACK_VA, stack, pagetable.PAGE_BYTES, true, false)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  raw.store_u64(frame + 248 as usize, (STACK_VA + pagetable.PAGE_BYTES) as u64);
  raw.store_u64(frame + 256 as usize, UVA as u64);
  raw.store_u64(frame + 264 as usize, 0 as u64);
  // The process gets its own TPIDR_EL0, and FPCR with flush-to-zero.
  raw.store_u64(frame + 272 as usize, 99 as u64);
  raw.store_u64(frame + 288 as usize, 0x1000000 as u64);
  unsafe {
    __sysreg_write("tpidr_el0", 1234i64);
    __sysreg_write("fpcr", 0i64);
  }
  set_reg(frame, 2, text as i64);
  let traps = 0;
  while (traps < 4) {
    let kind = 0i64;
    let esr = 0i64;
    let far = 0i64;
    unsafe {
      kind = __user_run(frame);
      esr = __sysreg_read("esr_el1");
      far = __sysreg_read("far_el1");
    }
    let ec = esr >> 26i64 & 63i64;
    if (kind == 0i64 && ec == 0x15i64) {
      print("svc " + reg(frame, 8).to_string() + " x0 " + reg(frame, 0).to_string());
      set_reg(frame, 0, 40i64);
    } else {
      print("trap kind " + kind.to_string() + " ec " + ec.to_string() + " far is the kernel address " + (far == text as i64).to_string());
      unsafe {
        print("kernel tpidr " + __sysreg_read("tpidr_el0").to_string() + " fpcr " + __sysreg_read("fpcr").to_string() + ", user tpidr " + raw.load_u64(frame + 272 as usize).to_string() + " fpcr " + raw.load_u64(frame + 288 as usize).to_string());
      }
      board.power_off(0);
      return 0;
    }
    traps += 1;
  }
  print("no fault");
  return 1;
}
`

func TestSelfHostKernelUserModeOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, userModeMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, qemuVirtArgs(out)...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	// x0 is 41 at the exit_group svc: the kernel answered the write with 40,
	// and the user code added 1. The data abort from EL0 has the class 0x24.
	for _, want := range []string{"svc 64 x0 7\n", "svc 93 x0 41\n", "trap kind 0 ec 36 far is the kernel address true\n", fmt.Sprintf("kernel tpidr 1234 fpcr 0, user tpidr 99 fpcr %d\n", 0x1000000)} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

const userRunSrc = `function main(): i32 {
  unsafe {
    return __user_run(0x1000 as usize) as i32;
  }
}
`

// The vector table sends the four EL0 slots to the trap entries, and the
// reset entry installs it even with no @interrupt handler.
func TestSelfHostUserRunVectors(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, userRunSrc)
	for _, want := range []string{"    msr vbar_el1, x9\n", "    bl __fern_user_run\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the image has no %q", want)
		}
	}
	wantInOrder(t, "arm64-kernel", text, []string{
		"__fern_vectors:\n",
		"__fern_user_run:\n    sub sp, sp, #192\n",
		"    mrs x10, tpidr_el0\n    mrs x11, fpcr\n    stp x10, x11, [sp, #160]\n",
		"    msr daifset, #15\n",
		"    ldp x0, x1, [x0, #0]\n    eret\n",
		"__fern_user_trap_0:\n    stp x0, x1, [sp, #-16]!\n",
		"__fern_user_trap_3:\n",
		".Luser_return:\n",
		"    ldp x10, x11, [sp, #160]\n    msr tpidr_el0, x10\n    msr fpcr, x11\n",
		"    msr daif, x10\n    ret\n",
	})
	i := strings.Index(text, "__fern_vectors:\n")
	var slots []string
	for _, line := range strings.Split(text[i:], "\n") {
		if strings.HasPrefix(line, "    b ") {
			slots = append(slots, strings.TrimPrefix(line, "    b "))
		}
		if len(slots) == 16 {
			break
		}
	}
	for k, got := range slots {
		want := ".Lvector_park"
		if k >= 8 && k < 12 {
			want = "__fern_user_trap_" + string(rune('0'+k-8))
		}
		if got != want {
			t.Errorf("vector slot %d branches to %q, want %q", k, got, want)
		}
	}
}

// The first slice of milestone 4 on x86-64: __user_run enters ring 3 with the
// state of a trap frame. The user code makes two syscalls, which the kernel
// answers through the frame. Then it loads a kernel address, and it jumps to
// its stack page, which is not executable. Both are page faults. At the end
// the kernel gives a PC outside the low half, which __user_run answers as a
// general-protection fault.
const userModeX86MainSrc = `import "./board";
import "std/pvh";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";

// The user window: the process's code at UVA and its stack page above it.
const UVA: usize = 0x1000000000;

const STACK_VA: usize = 0x1000010000;

// edi = 7; eax = 1; syscall; edi = eax + 1; eax = 231; syscall; load from
// rdx; jump to rbx.
function code(): u8[] {
  return [0xbfu8, 7u8, 0u8, 0u8, 0u8, 0xb8u8, 1u8, 0u8, 0u8, 0u8, 0x0fu8, 0x05u8, 0x8du8, 0x78u8, 1u8, 0xb8u8, 0xe7u8, 0u8, 0u8, 0u8, 0x0fu8, 0x05u8, 0x48u8, 0x8bu8, 0x0au8, 0xffu8, 0xe3u8];
}

function reg(frame: usize, n: i32): i64 {
  return raw.load_u64(frame + (8 * n) as usize) as i64;
}

function set_reg(frame: usize, n: i32, v: i64): void {
  raw.store_u64(frame + (8 * n) as usize, v as u64);
}

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
  let heap_frames = f.free_count() - 256 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  let Ok(info) = pvh.parse(board.start_info()) else {
    print("no start info");
    return 1;
  };
  let Ok(regions) = pvh.parse_memmap(board.memmap(info), info.memmap_entries) else {
    print("no memory map");
    return 1;
  };
  let Some(tables) = pagetable.new(f) else {
    return 1;
  };
  let Some(text) = f.alloc() else {
    return 1;
  };
  let Some(stack) = f.alloc() else {
    return 1;
  };
  let Some(frame) = f.alloc() else {
    return 1;
  };
  let bytes = code();
  for i in 0..bytes.len() {
    raw.store_u8(text + i as usize, bytes[i]);
  }
  let i = 0 as usize;
  while (i < frames.FRAME_BYTES) {
    raw.store_u64(frame + i, 0 as u64);
    i += 8 as usize;
  }
  if (!map_all(tables, regions) || !tables.map_user(UVA, text, pagetable.PAGE_BYTES, false, true) || !tables.map_user(STACK_VA, stack, pagetable.PAGE_BYTES, true, false)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  raw.store_u64(frame + 32 as usize, (STACK_VA + pagetable.PAGE_BYTES) as u64);
  raw.store_u64(frame + 128 as usize, UVA as u64);
  // The process gets its own FS base, and MXCSR with flush-to-zero.
  raw.store_u64(frame + 144 as usize, 99 as u64);
  raw.store_u64(frame + 184 as usize, 0x9f80 as u64);
  unsafe {
    __sysreg_write("fs_base", 1234i64);
  }
  set_reg(frame, 2, text as i64);
  set_reg(frame, 3, STACK_VA as i64);
  let faults = 0;
  let traps = 0;
  while (traps < 5) {
    let kind = 0i64;
    unsafe {
      kind = __user_run(frame);
    }
    if (kind == 0i64) {
      print("syscall " + reg(frame, 0).to_string() + " rdi " + reg(frame, 7).to_string());
      set_reg(frame, 0, 40i64);
    } else if (faults == 0) {
      // The load of 3 bytes faults on the kernel address. Step over it.
      print("trap kind " + kind.to_string() + " vector " + reg(frame, 20).to_string() + " error " + reg(frame, 21).to_string() + " cr2 is the kernel address " + (reg(frame, 22) == text as i64).to_string());
      set_reg(frame, 16, reg(frame, 16) + 3i64);
      faults = 1;
    } else if (faults == 1) {
      // The stack page is not executable. Then give a PC outside the low half.
      print("trap kind " + kind.to_string() + " vector " + reg(frame, 20).to_string() + " error " + reg(frame, 21).to_string() + " cr2 is the stack " + (reg(frame, 22) == STACK_VA as i64).to_string());
      set_reg(frame, 16, 0x800000000000i64);
      faults = 2;
    } else {
      print("trap kind " + kind.to_string() + " vector " + reg(frame, 20).to_string() + " error " + reg(frame, 21).to_string() + " for a PC outside the low half");
      unsafe {
        print("kernel fs_base " + __sysreg_read("fs_base").to_string() + ", user fs_base " + reg(frame, 18).to_string() + " mxcsr " + reg(frame, 23).to_string());
      }
      return 0;
    }
    traps += 1;
  }
  print("no fault");
  return 1;
}
`

// The maps board has no page-fault handler of its own after the cut, so each
// fault that the console shows came through the ring 3 gate.
var userModeX86BoardSrc = mapsX86BoardSrc[:strings.Index(mapsX86BoardSrc, "// A page fault ends the run")]

func TestSelfHostKernelX86UserModeOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, userModeX86BoardSrc, userModeX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("qemu: %v, want exit status 1 from isa-debug-exit; console:\n%s", err, console)
	}
	// %rdi is 41 at the exit_group syscall: the kernel answered the write with
	// 40, and the user code added 1. The error code 5 is a read of a present
	// page from ring 3, and 21 is an instruction fetch of one. MXCSR 40832 is
	// the reset value with flush-to-zero.
	for _, want := range []string{
		"syscall 1 rdi 7\n",
		"syscall 231 rdi 41\n",
		"trap kind 2 vector 14 error 5 cr2 is the kernel address true\n",
		"trap kind 2 vector 14 error 21 cr2 is the stack true\n",
		"trap kind 2 vector 13 error 0 for a PC outside the low half\n",
		"kernel fs_base 1234, user fs_base 99 mxcsr 40832\n",
	} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// The x86-64 image has a gate for each vector, a GDT with the ring 3
// segments, a TSS, and the SYSCALL registers, even with no @interrupt handler.
func TestSelfHostUserRunX86Gates(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, userRunSrc)
	wantInOrder(t, "x86-64-kernel", text, []string{
		"    leaq __fern_user_gates(%rip), %rax\n",
		"    lidt (%rsp)\n",
		"    call __fern_user_init\n",
		"__fern_user_init:\n",
		"    lgdt (%rsp)\n    movl $48, (%rsp)\n    ltr (%rsp)\n",
		"__fern_user_run:\n    pushfq\n",
		"    jnz .Luser_bad_pc\n",
		"    movq %rsp, __fern_user_tss+4(%rip)\n",
		"    andl $3285, %eax\n    orl $514, %eax\n    pushq %rax\n    pushq $43\n",
		"    iretq\n",
		"__fern_user_syscall:\n    movq %rcx, __fern_user_ctx+16(%rip)\n",
		"__fern_user_gates:\n",
		"    testb $3, 8(%rsp)\n    jz .Lvector_park\n    pushq $0\n    pushq $0\n    jmp __fern_user_trap\n",
		"    testb $3, 16(%rsp)\n    jz .Lvector_park\n    pushq $14\n    jmp __fern_user_trap\n",
		"    pushq $255\n    jmp __fern_user_trap\n",
		"__fern_user_trap:\n    pushq %rdi\n",
		".Luser_leave:\n    cld\n",
		".Luser_ret:\n",
		"    popfq\n    ret\n",
	})
	if !strings.Contains(text, "    call __fern_user_run\n") {
		t.Errorf("main does not call __fern_user_run")
	}
	if n := strings.Count(text, "    jmp __fern_user_trap\n"); n != 256 {
		t.Errorf("the image has %d ring 3 gates, want 256", n)
	}
}

// A hosted target refuses the intrinsic at the capability gate. A call outside
// an unsafe block is E095, as for __sysreg_write.
func TestSelfHostUserRunRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(userRunSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	const outside = "function main(): i32 {\n  return __user_run(0x1000 as usize) as i32;\n}\n"
	bare := filepath.Join(dir, "outside.fern")
	if err := os.WriteFile(bare, []byte(outside), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("arm64-kernel", filepath.Join(dir, "outside.elf"), bare, "-ram", kernelRAM).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E095") || !strings.Contains(string(msg), "__user_run") {
		t.Errorf("outside unsafe: err %v; want E095 for __user_run in:\n%s", err, msg)
	}
	if got := goCheckerCodes(t, t.TempDir(), outside); !slices.Contains(got, "E095") {
		t.Errorf("outside unsafe: Go checker codes = %v, want E095", got)
	}
	if msg, err = cli.compileCmd("x86-64-kernel", filepath.Join(dir, "x86.elf"), src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Errorf("x86-64-kernel: %v\n%s", err, msg)
	}
	msg, err = cli.compileCmd("arm64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "__user_run") {
		t.Errorf("arm64-linux: err %v; want E066 for __user_run in:\n%s", err, msg)
	}
}
