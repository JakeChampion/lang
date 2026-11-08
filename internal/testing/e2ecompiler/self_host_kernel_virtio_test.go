package e2ecompiler

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The virtio-net driver (#14259, the first user of the DMA region of #14255):
// the image opens the virtio-net device of the VM over std/virtio_net, sends
// an ARP request for the gateway of qemu's user network and an ICMP echo to
// it, and receives both replies. The probe runs on the kernel mapping of the
// maps test, with the rings and the frame slots in std/dma buffers.
const virtioProbeSrc = `// The addresses of qemu's user network: the guest and its gateway.
function own_ip(): u8[] {
  return [10u8, 0u8, 2u8, 15u8];
}

function gateway_ip(): u8[] {
  return [10u8, 0u8, 2u8, 2u8];
}

function mac_text(m: u8[]): string {
  let s = "";
  for i in 0..m.len() {
    if (i > 0) {
      s += ":";
    }
    s += hex.encode([m[i]]);
  }
  return s;
}

function be16(v: i32): u8[] {
  return [(v >> 8 & 255) as u8, (v & 255) as u8];
}

// The ones' complement sum of the bytes from start to end, as IPv4 and ICMP
// checksum their headers.
function checksum(data: u8[], start: i32, end: i32): i32 {
  let sum = 0;
  let i = start;
  while (i + 1 < end) {
    sum += data[i] as i32 << 8 | data[i + 1] as i32;
    i += 2;
  }
  if (i < end) {
    sum += data[i] as i32 << 8;
  }
  while (sum >> 16 != 0) {
    sum = (sum & 0xffff) + (sum >> 16);
  }
  return sum ^ 0xffff;
}

function ethernet(dst: u8[], src: u8[], kind: i32): u8[] {
  let f: u8[] = [];
  for b in dst {
    f = f.append(b);
  }
  for b in src {
    f = f.append(b);
  }
  for b in be16(kind) {
    f = f.append(b);
  }
  return f;
}

// An ARP request for the gateway.
function arp_request(mac: u8[]): u8[] {
  let f = ethernet([255u8, 255u8, 255u8, 255u8, 255u8, 255u8], mac, 0x0806);
  for b in [0u8, 1u8, 8u8, 0u8, 6u8, 4u8, 0u8, 1u8] {
    f = f.append(b);
  }
  for b in mac {
    f = f.append(b);
  }
  for b in own_ip() {
    f = f.append(b);
  }
  for b in [0u8, 0u8, 0u8, 0u8, 0u8, 0u8] {
    f = f.append(b);
  }
  for b in gateway_ip() {
    f = f.append(b);
  }
  return f;
}

// An ICMP echo request to the gateway, sequence 1, with the payload "fern".
function icmp_echo(mac: u8[], gateway: u8[]): u8[] {
  let f = ethernet(gateway, mac, 0x0800);
  let ip: u8[] = [0x45u8, 0u8, 0u8, 32u8, 0u8, 1u8, 0u8, 0u8, 64u8, 1u8, 0u8, 0u8];
  for b in own_ip() {
    ip = ip.append(b);
  }
  for b in gateway_ip() {
    ip = ip.append(b);
  }
  let c = checksum(ip, 0, ip.len());
  ip = ip.with(10, (c >> 8) as u8);
  ip = ip.with(11, (c & 255) as u8);
  let icmp: u8[] = [8u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 1u8, 102u8, 101u8, 114u8, 110u8];
  let ic = checksum(icmp, 0, icmp.len());
  icmp = icmp.with(2, (ic >> 8) as u8);
  icmp = icmp.with(3, (ic & 255) as u8);
  for b in ip {
    f = f.append(b);
  }
  for b in icmp {
    f = f.append(b);
  }
  return f;
}

// Polls the device for a frame of the Ethernet type kind, for a bounded
// number of turns.
function wait_frame(n: virtio_net.Net, kind: i32): Option[u8[]] {
  let turns = 0;
  while (turns < 50000000) {
    match (n.receive()) {
      Some(frame) => {
        if (frame.len() >= 14 && (frame[12] as i32 << 8 | frame[13] as i32) == kind) {
          return Some(frame);
        }
      },
      None => {}
    }
    turns += 1;
  }
  return None;
}

function probe_network(n: virtio_net.Net): string {
  print("virtio-net mac " + mac_text(n.mac));
  if (!n.send(arp_request(n.mac))) {
    return "arp request not sent";
  }
  let Some(reply) = wait_frame(n, 0x0806) else {
    return "no arp reply";
  };
  if (reply.len() < 42 || reply[21] as i32 != 2) {
    return "not an arp reply";
  }
  let gateway: u8[] = [];
  for i in 22..28 {
    gateway = gateway.append(reply[i]);
  }
  print("arp reply from " + mac_text(gateway) + " for " + (reply[28] as i32).to_string() + "." + (reply[29] as i32).to_string() + "." + (reply[30] as i32).to_string() + "." + (reply[31] as i32).to_string());
  if (!n.send(icmp_echo(n.mac, gateway))) {
    return "icmp echo not sent";
  }
  let Some(echo) = wait_frame(n, 0x0800) else {
    return "no icmp reply";
  };
  if (echo.len() < 46 || echo[23] as i32 != 1 || echo[34] as i32 != 0) {
    return "not an icmp echo reply";
  }
  let bytes: u8[] = [];
  for i in 42..46 {
    bytes = bytes.append(echo[i]);
  }
  let payload = "";
  unsafe {
    payload = string_from_bytes_unchecked(bytes);
  }
  return "icmp echo reply seq " + (echo[40] as i32 << 8 | echo[41] as i32).to_string() + " payload " + payload;
}
`

const virtioMainSrc = `import "./board";
import "std/fdt";
import "std/frames";
import "std/hex";
import "std/option";
import "std/pagetable";
import "std/virtio";
import "std/virtio_net";

` + virtioProbeSrc + `
// The first virtio-mmio transport of the device tree with a network device.
function find_net(t: fdt.Tree): Option[virtio.Device] {
  for i in 0..t.nodes.len() {
    for s in t.property_strings(i, "compatible") {
      if (s == "virtio,mmio") {
        let Some(r) = t.reg(i, 0) else {
          continue;
        };
        match (virtio.probe(r.address as usize)) {
          Some(d) => {
            if (d.id == virtio.ID_NET) {
              return Some(d);
            }
          },
          None => {}
        }
      }
    }
  }
  return None;
}

function main(): i32 {
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
  if (!tables.map_normal(r.address as usize, r.size as usize) || !tables.map_device(board.DEVICES, board.DEVICES_BYTES)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  let Some(d) = find_net(t) else {
    print("no virtio-net device");
    return 1;
  };
  let Some(n) = virtio_net.open(d, f, tables) else {
    print("virtio-net did not open");
    return 1;
  };
  print(probe_network(n));
  return 0;
}
`

const virtioX86MainSrc = `import "./board";
import "std/frames";
import "std/hex";
import "std/option";
import "std/pagetable";
import "std/pvh";
import "std/virtio";
import "std/virtio_net";

` + virtioProbeSrc + `
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

// The first of the eight virtio-mmio transports of qemu microvm with a
// network device.
function find_net(): Option[virtio.Device] {
  for i in 0..8 {
    match (virtio.probe(board.VIRTIO + i as usize * 512 as usize)) {
      Some(d) => {
        if (d.id == virtio.ID_NET) {
          return Some(d);
        }
      },
      None => {}
    }
  }
  return None;
}

function main(): i32 {
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
  let heap_frames = f.free_count() - 256 as usize;
  let Some(heap) = f.alloc_run(heap_frames) else {
    print("no heap run");
    return 1;
  };
  unsafe {
    __heap_adopt(heap as i64, (heap + heap_frames * frames.FRAME_BYTES) as i64);
  }
  let Some(tables) = pagetable.new(f) else {
    print("no root frame");
    return 1;
  };
  if (!map_all(tables, regions)) {
    print("no mapping");
    return 1;
  }
  tables.activate();
  let Some(d) = find_net() else {
    print("no virtio-net device");
    return 1;
  };
  let Some(n) = virtio_net.open(d, f, tables) else {
    print("virtio-net did not open");
    return 1;
  };
  print(probe_network(n));
  return 16;
}
`

// qemu's user network gives the guest 10.0.2.15, answers for the gateway
// 10.0.2.2 with the MAC 52:55:0a:00:02:02, and gives the first NIC the MAC
// 52:54:00:12:34:56.
var virtioWantLines = []string{
	"virtio-net mac 52:54:00:12:34:56\n",
	"arp reply from 52:55:0a:00:02:02 for 10.0.2.2\n",
	"icmp echo reply seq 1 payload fern\n",
}

// The virtio-mmio transport of the VM is version 2 only on request: qemu
// keeps the legacy layout unless force-legacy is off.
func virtioNetArgs() []string {
	return []string{"-global", "virtio-mmio.force-legacy=false", "-netdev", "user,id=n0", "-device", "virtio-net-device,netdev=n0"}
}

func TestSelfHostKernelVirtioNetOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, virtioMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-kernel", out}
	console, err := bootQemu(t, qemu, append(args, virtioNetArgs()...)...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range virtioWantLines {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// main returns 16, so qemu exits with 33 from isa-debug-exit. Without ACPI
// the microvm has eight virtio-mmio transports, and qemu puts the device on
// the last free one, slot 7.
func TestSelfHostKernelX86VirtioNetOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, virtioX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	args := []string{"-M", "microvm,acpi=off", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh"}
	console, err := bootQemu(t, qemu, append(args, virtioNetArgs()...)...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	for _, want := range virtioWantLines {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
