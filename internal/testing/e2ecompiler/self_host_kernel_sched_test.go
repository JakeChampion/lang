package e2ecompiler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Milestone 4 of docs/BARE-METAL-PLAN.md (#14263) on arm64: processes with
// separate heaps run under a timer that preempts them. Each tick during EL0
// returns to the kernel loop, which runs the next process. The kernel heap
// is never preempted, because the top half touches no RC value.
const schedMainSrc = processKernelSrc + `
struct Slot { path: string, p: user_process.Process, line: u8[], preempts: i32, done: boolean }

// Runs the slot until its next event, and gives the slot after it.
function step(b: Boot, s: Slot): Slot {
  let (q, stop) = s.p.run();
  let next = Slot { ...s, p: q };
  match (stop) {
    Output(fd, bytes) => {
      return Slot { ...next, line: print_lines(s.path, fd, s.line, bytes) };
    },
    Exited(status) => {
      print(s.path + " exit " + status.to_string());
    },
    Killed(why) => {
      print(s.path + " killed: " + why);
    },
    Interrupted => {
      return Slot { ...next, preempts: s.preempts + 1 };
    }
  }
  b.kernel.activate();
  q.release();
  return Slot { ...next, done: true };
}

function main(): i32 {
  let Some(b) = boot(4096 as usize) else {
    print("no boot");
    return 1;
  };
  let free = b.f.free_count();
  let slots: Slot[] = [];
  for argv in [["/bin/worker", "a"], ["/bin/worker", "b"], ["/bin/efault"], ["/bin/peek"], ["/bin/norandom"]] {
    let Some(p) = load(b, argv[0], argv) else {
      return 1;
    };
    let label = argv[0];
    if (argv.len() > 1) {
      label = label + " " + argv[1];
    }
    slots = slots.append(Slot { path: label, p: p, line: [], preempts: 0, done: false });
  }
  board.gic_init();
  board.timer_start();
  unsafe {
    __irq_unmask();
  }
  let live = slots.len();
  while (live > 0) {
    for i in 0..slots.len() {
      if (!slots[i].done) {
        let next = step(b, slots[i]);
        if (next.done) {
          live -= 1;
        }
        slots = slots.with(i, next);
      }
    }
  }
  unsafe {
    __irq_mask();
  }
  for s in slots {
    print(s.path + " preempted " + (s.preempts > 0).to_string());
  }
  print("ticks " + (board.irq_count() > 0i64).to_string() + ", frames back " + (b.f.free_count() == free).to_string());
  board.power_off(0);
  return 0;
}
`

// The worker allocates and frees strings in a loop, and checks the total of
// their lengths: 200 names of one letter and the numbers 0 to 199, 300 times.
const schedWorkerSrc = `function main(): i32 {
  let argv = args();
  let name = argv[1];
  let total = 0;
  for round in 0..300 {
    let parts: string[] = [];
    for i in 0..200 {
      parts = parts.append(name + i.to_string());
    }
    for p in parts {
      total += p.len();
    }
  }
  print(name + " total " + total.to_string());
  if (total != 300 * 690) {
    return 1;
  }
  return 0;
}
`

// peekImage lies at 0x800000 and loads from 0x400000, where the worker has
// its code. That address is not in the tables of peek, so the load faults.
func peekImage() []byte {
	return codeImage(0x800000, []uint32{
		0xd2a00801, // movz x1, #0x40, lsl #16
		0xf9400020, // ldr x0, [x1]
		0xd2800000, // movz x0, #0
		0xd2800bc8, // movz x8, #94
		0xd4000001, // svc #0
	})
}

// norandomImage calls getrandom into its stack and exits with the negated
// result. The VM of the scheduler test has no entropy device, so the process
// has no source, and the status is ENOSYS.
func norandomImage() []byte {
	return codeImage(0x400000, []uint32{
		0xd10103e0, // sub x0, sp, #64
		0xd2800201, // movz x1, #16
		0xd2800002, // movz x2, #0
		0xd28022c8, // movz x8, #278
		0xd4000001, // svc #0
		0xcb0003e0, // neg x0, x0
		0xd2800bc8, // movz x8, #94
		0xd4000001, // svc #0
	})
}

func TestSelfHostKernelSchedulerOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrd(t, cli, dir, map[string]string{"worker": schedWorkerSrc}, map[string][]byte{"efault": efaultImage(), "peek": peekImage(), "norandom": norandomImage()})
	src := writeTickProgram(t, processBoardSrc, schedMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	out, err := bootQemu(t, qemu, "-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "256M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive)
	console := string(out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range []string{
		"/bin/worker a fd 1: a total 207000\n",
		"/bin/worker b fd 1: b total 207000\n",
		"/bin/worker a exit 0\n",
		"/bin/worker b exit 0\n",
		"/bin/efault exit 14\n",
		"/bin/peek killed: a fault of class 36 at 4194304 from ",
		"/bin/worker a preempted true\n",
		"/bin/worker b preempted true\n",
		"/bin/norandom exit 38\n",
		"ticks true, frames back true\n",
	} {
		if !strings.Contains(console, want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
	// The bad syscall and the read of the other process end their processes
	// while the workers still run, and the workers then finish.
	for _, early := range []string{"/bin/efault exit 14\n", "/bin/peek killed"} {
		for _, late := range []string{"a total", "b total"} {
			if i, j := strings.Index(console, early), strings.Index(console, late); i < 0 || j < 0 || i > j {
				t.Errorf("console:\n%s\nwant %q before %q", console, early, late)
			}
		}
	}
}
