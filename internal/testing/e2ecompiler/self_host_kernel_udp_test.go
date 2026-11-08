package e2ecompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The first layers of the network stack (#14259): the image runs a UDP echo
// server on port 7 over std/ip. qemu's user network forwards a host port to
// it, so the test sends datagrams from the host and reads them back. On the
// way, std/ip answers the ARP request that the user network makes for the
// guest's address and learns the sender's MAC from the datagram.
const udpEchoSrc = `// Echoes three datagrams on port 7, then stops.
function serve(iface: ip.Interface): string {
  let echoes = 0;
  let turns = 0;
  while (echoes < 3 && turns < 400000000) {
    match (iface.poll()) {
      Some(p) => {
        match (p) {
          Udp(d) => {
            if (d.port == 7) {
              print("datagram from " + (d.source[0] as i32).to_string() + "." + (d.source[1] as i32).to_string() + "." + (d.source[2] as i32).to_string() + "." + (d.source[3] as i32).to_string() + " of " + d.payload.len().to_string() + " bytes");
              if (iface.send_udp(d.source, d.source_port, 7, d.payload)) {
                echoes += 1;
              } else {
                print("no route back");
              }
            }
          },
          Tcp(s) => {}
        }
      },
      None => {}
    }
    turns += 1;
  }
  return "udp echoes " + echoes.to_string();
}
`

// kernelNetMain is the main module of a network test on qemu-virt. It builds
// the frames and the kernel mapping, opens the virtio-net device of the
// device tree over std/virtio_net, and opens a std/ip interface at 10.0.2.15
// with the gateway of qemu's user network. `run` then uses `iface`.
func kernelNetMain(imports, body, run string) string {
	return `import "./board";
import "std/fdt";
import "std/frames";
import "std/ip";
import "std/option";
import "std/pagetable";
import "std/virtio";
import "std/virtio_net";
` + imports + `
` + body + `
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
  let Some(page) = f.alloc() else {
    print("no page for the arp cache");
    return 1;
  };
  let iface = ip.open(n, [10u8, 0u8, 2u8, 15u8], [10u8, 0u8, 2u8, 2u8], page);
` + run + `
  return 0;
}
`
}

// kernelNetX86Main is kernelNetMain for qemu microvm: the RAM comes from the
// PVH memory map, and the device is on one of the eight virtio-mmio
// transports. main returns 16, so qemu exits with 33 from isa-debug-exit.
func kernelNetX86Main(imports, body, run string) string {
	return `import "./board";
import "std/frames";
import "std/ip";
import "std/option";
import "std/pagetable";
import "std/pvh";
import "std/virtio";
import "std/virtio_net";
` + imports + `
` + body + `
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
  let Some(page) = f.alloc() else {
    print("no page for the arp cache");
    return 1;
  };
  let iface = ip.open(n, [10u8, 0u8, 2u8, 15u8], [10u8, 0u8, 2u8, 2u8], page);
` + run + `
  return 16;
}
`
}

const udpRun = `  print("listening on udp 7");
  print(serve(iface));`

var udpMainSrc = kernelNetMain("", udpEchoSrc, udpRun)

var udpX86MainSrc = kernelNetX86Main("", udpEchoSrc, udpRun)

// freeUDPPort takes a port from the kernel and gives it back, so qemu can
// bind it a moment later.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}

// echoThrough boots the VM with the host port forwarded to the guest's UDP
// port 7, sends three datagrams until each comes back, and returns the
// console and qemu's exit error. A datagram the guest is not yet listening
// for is lost in the user network, so each one is sent again until its echo
// arrives.
func echoThrough(t *testing.T, qemu string, port int, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, args...)
	var console bytes.Buffer
	cmd.Stdout = &console
	cmd.Stderr = &console
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for n := 1; n <= 3; n++ {
		want := fmt.Sprintf("fern %d", n)
		got := ""
		for attempt := 0; attempt < 200 && got != want; attempt++ {
			if _, err := conn.Write([]byte(want)); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			buf := make([]byte, 64)
			k, err := conn.Read(buf)
			if err == nil {
				got = string(buf[:k])
			} else {
				// Before qemu binds the port, the kernel refuses the datagram
				// at once; pace the retries so the VM has time to boot.
				time.Sleep(250 * time.Millisecond)
			}
		}
		if got != want {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("datagram %d never came back; console:\n%s", n, console.Bytes())
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not stop in 120s; console:\n%s", console.Bytes())
	}
	return console.Bytes(), err
}

func TestSelfHostKernelUDPEchoOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, udpMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	port := freeUDPPort(t)
	args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-kernel", out,
		"-global", "virtio-mmio.force-legacy=false",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=udp:127.0.0.1:%d-10.0.2.15:7", port),
		"-device", "virtio-net-device,netdev=n0"}
	console, err := echoThrough(t, qemu, port, args...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range []string{"listening on udp 7\n", "datagram from 10.0.2.2 of 6 bytes\n", "udp echoes 3\n"} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// main returns 16, so qemu exits with 33 from isa-debug-exit.
func TestSelfHostKernelX86UDPEchoOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, udpX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	port := freeUDPPort(t)
	args := []string{"-M", "microvm,acpi=off", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh",
		"-global", "virtio-mmio.force-legacy=false",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=udp:127.0.0.1:%d-10.0.2.15:7", port),
		"-device", "virtio-net-device,netdev=n0"}
	console, err := echoThrough(t, qemu, port, args...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	for _, want := range []string{"listening on udp 7\n", "datagram from 10.0.2.2 of 6 bytes\n", "udp echoes 3\n"} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
