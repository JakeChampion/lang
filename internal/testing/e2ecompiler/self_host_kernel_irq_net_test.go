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

// The fourth slice of the network stack (#14259): the HTTP server of the
// kernel sleeps on the IRQ line of the network device and on the timer line,
// through std/async, instead of polling. The top half of the board sets the
// line's bit. The bottom half acknowledges the device, takes every frame the
// listener has, and answers. The device's line is edge-triggered on both
// machines, so a buffer the device returns after the acknowledgement raises
// it again.
const kernelIRQServeSrc = `function push_all(data: u8[], more: u8[]): u8[] {
  for b in more {
    data = data.append(b);
  }
  return data;
}

// Answers three requests, each on its own connection, from wakes on the
// device's line and the timer's line, then stops once every connection is
// gone. It counts the wakes of each line.
function serve(l: tcp.Listener, n: virtio_net.Net, net_line: i32): string {
  let pending: u8[][] = [[], [], [], [], [], [], [], []];
  let answered = 0;
  let net_wakes = 0;
  let timer_wakes = 0;
  while (answered < 3 || l.active() > 0) {
    let Some(woke) = async.block_on(async.race([async.irq(net_line), async.irq(board.TIMER_IRQ)])) else {
      return "cancelled";
    };
    let (which, line) = woke;
    if (line == net_line) {
      net_wakes += 1;
      let used = n.interrupt();
    } else {
      timer_wakes += 1;
    }
    let (polled, events) = l.poll(board.irq_count());
    l = polled;
    for e in events {
      match (e) {
        Accepted(id) => {
          pending = pending.with(id, []);
        },
        Data(id, bytes) => {
          let buf = push_all(pending[id], bytes);
          pending = pending.with(id, buf);
          match (http.parse_request_framed(buf)) {
            Complete(f) => {
              print("request " + f.request.method + " " + f.request.path);
              l = l.write(id, http.serialize_response_conn_bytes(http.ok("hello from fern"), false));
              l = l.close(id);
              pending = pending.with(id, []);
              answered += 1;
            },
            Incomplete => {},
            Continue => {},
            Malformed(code) => {
              print("malformed request " + code.to_string());
              l = l.close(id);
            }
          }
        },
        Finished(id) => {},
        Closed(id) => {}
      }
    }
  }
  return "http requests " + answered.to_string() + ", net wakes at least 3 " + (net_wakes >= 3).to_string() + ", timer wakes at least 3 " + (timer_wakes >= 3).to_string();
}
`

// The IRQ line of the virtio-mmio transport at base, from the interrupts
// property of its node: GIC_SPI, the SPI number, and the edge flag. SPI n is
// line 32 + n.
const kernelIRQLineArm64Src = `function net_line(t: fdt.Tree, base: usize): i32 {
  for i in 0..t.nodes.len() {
    let Some(r) = t.reg(i, 0) else {
      continue;
    };
    if (r.address as usize != base) {
      continue;
    }
    let Some(p) = t.property(i, "interrupts") else {
      continue;
    };
    if (p.len < 8) {
      continue;
    }
    return 32 + (t.blob[p.start + 6] as i32 << 8 | t.blob[p.start + 7] as i32);
  }
  return -1;
}
`

// The IOAPIC input of the virtio-mmio transport at base, from the entry
// "virtio_mmio.device=512@0xBASE:GSI" that qemu microvm puts on the command
// line without ACPI.
const kernelIRQLineX86Src = `function hex_digit(c: u8): i32 {
  if (c >= 48u8 && c <= 57u8) {
    return c as i32 - 48;
  }
  if (c >= 97u8 && c <= 102u8) {
    return c as i32 - 87;
  }
  return -1;
}

function net_gsi(line: string, base: usize): i32 {
  let bytes = line.bytes();
  let i = 0;
  while (i + 2 < bytes.len()) {
    if (bytes[i] != 64u8 || bytes[i + 1] != 48u8 || bytes[i + 2] != 120u8) {
      i += 1;
      continue;
    }
    let at = i + 3;
    let address = 0 as usize;
    while (at < bytes.len() && hex_digit(bytes[at]) >= 0) {
      address = address * 16 as usize + hex_digit(bytes[at]) as usize;
      at += 1;
    }
    if (address == base && at + 1 < bytes.len() && bytes[at] == 58u8) {
      let gsi = 0;
      at += 1;
      while (at < bytes.len() && bytes[at] >= 48u8 && bytes[at] <= 57u8) {
        gsi = gsi * 10 + (bytes[at] as i32 - 48);
        at += 1;
      }
      return gsi;
    }
    i = at;
  }
  return -1;
}
`

const kernelIRQImports = `import "std/async";
import "std/http";
import "std/tcp";
`

var kernelIRQMainSrc = kernelNetMain(kernelIRQImports, kernelIRQServeSrc+kernelIRQLineArm64Src, `  let line = net_line(t, d.base);
  if (line < 32) {
    print("no interrupt line for the device");
    return 1;
  }
  board.gic_init();
  board.gic_enable(line);
  board.timer_start();
  __irq_unmask();
  let l = tcp.listen(iface, 80, 1000 as u32);
  print("listening on tcp 80 with irq line " + line.to_string());
  print(serve(l, n, line));`)

var kernelIRQX86MainSrc = kernelNetX86Main(kernelIRQImports, kernelIRQServeSrc+kernelIRQLineX86Src, `  let gsi = net_gsi(cmdline, d.base);
  if (gsi < 0) {
    print("no interrupt line for the device");
    return 1;
  }
  board.apic_init();
  board.ioapic_route(gsi);
  __irq_unmask();
  let l = tcp.listen(iface, 80, 1000 as u32);
  print("listening on tcp 80 with irq line " + board.NET_IRQ.to_string() + " from gsi " + gsi.to_string());
  print(serve(l, n, board.NET_IRQ));`)

var kernelIRQWantLines = []string{"request GET /hello1\n", "request GET /hello3\n", "http requests 3, net wakes at least 3 true, timer wakes at least 3 true\n"}

func TestSelfHostKernelHTTPByInterruptOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, kernelIRQMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	port := freeTCPPort(t)
	args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-kernel", out,
		"-global", "virtio-mmio.force-legacy=false",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-10.0.2.15:80", port),
		"-device", "virtio-net-device,netdev=n0"}
	console, err := fetchHTTPThrough(t, qemu, port, args...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range append([]string{"listening on tcp 80 with irq line 79\n"}, kernelIRQWantLines...) {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// main returns 16, so qemu exits with 33 from isa-debug-exit.
func TestSelfHostKernelX86HTTPByInterruptOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, kernelIRQX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	port := freeTCPPort(t)
	args := []string{"-M", "microvm,acpi=off", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh",
		"-global", "virtio-mmio.force-legacy=false",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-10.0.2.15:80", port),
		"-device", "virtio-net-device,netdev=n0"}
	console, err := fetchHTTPThrough(t, qemu, port, args...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	for _, want := range append([]string{"listening on tcp 80 with irq line 33 from gsi 12\n"}, kernelIRQWantLines...) {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
