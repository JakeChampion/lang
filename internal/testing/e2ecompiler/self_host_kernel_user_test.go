package e2ecompiler

import (
	"os"
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
	for _, want := range []string{"svc 64 x0 7\n", "svc 93 x0 41\n", "trap kind 0 ec 36 far is the kernel address true\n"} {
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
		"__fern_user_run:\n    sub sp, sp, #160\n",
		"    msr daifset, #15\n",
		"    ldp x0, x1, [x0, #0]\n    eret\n",
		"__fern_user_trap_0:\n    stp x0, x1, [sp, #-16]!\n",
		"__fern_user_trap_3:\n",
		".Luser_return:\n",
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

// x86-64 enters ring 3 in a later slice. Until then the emitter refuses the op
// by name, and a hosted target refuses the intrinsic at the capability gate.
// A call outside an unsafe block is E095, as for __sysreg_write.
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
	msg, err = cli.compileCmd("x86-64-kernel", filepath.Join(dir, "x86.elf"), src, "-ram", kernelX86RAM).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "user_run") {
		t.Errorf("x86-64-kernel: err %v; want a refusal that names user_run in:\n%s", err, msg)
	}
	msg, err = cli.compileCmd("arm64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "__user_run") {
		t.Errorf("arm64-linux: err %v; want E066 for __user_run in:\n%s", err, msg)
	}
}
