package e2ecompiler

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// An unmodified std/serve program, built for arm64-linux, serves HTTP as a
// user process of the kernel (#15096). Its sockets, its epoll instance and
// its signalfd are std/user_process descriptors over a std/user_net listener
// on the virtio-net interface. When three connections have closed, the
// kernel sends SIGTERM, and the program stops cleanly.
var netProcessMainSrc = strings.Replace(processKernelSrc, "import \"std/virtio_rng\";\n", "import \"std/virtio_rng\";\nimport \"std/ip\";\nimport \"std/user_net\";\nimport \"std/virtio_net\";\n", 1) + `
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
  let Some(b) = boot(8192 as usize) else {
    print("no boot");
    return 1;
  };
  let Ok(t) = fdt.parse(board.device_tree()) else {
    print("no device tree");
    return 1;
  };
  let Some(d) = find_net(t) else {
    print("no virtio-net device");
    return 1;
  };
  let Some(n) = virtio_net.open(d, b.f, b.kernel) else {
    print("virtio-net did not open");
    return 1;
  };
  let Some(page) = b.f.alloc() else {
    print("no page for the arp cache");
    return 1;
  };
  let iface = ip.open(n, [10u8, 0u8, 2u8, 15u8], [10u8, 0u8, 2u8, 2u8], page);
  let free = b.f.free_count();
  let Some(started) = load(b, "/bin/srv", ["/bin/srv"]) else {
    return 1;
  };
  let p = started.with_net(user_net.new(iface, 1000 as u32));
  let line: u8[] = [];
  let stopping = false;
  let running = true;
  while (running) {
    let (q, stop) = p.run();
    p = q;
    match (stop) {
      Output(fd, bytes) => {
        line = print_lines("/bin/srv", fd, line, bytes);
      },
      Exited(status) => {
        print("/bin/srv exit " + status.to_string());
        running = false;
      },
      Killed(why) => {
        print("/bin/srv killed: " + why);
        running = false;
      },
      Interrupted => {},
      Waiting => {
        if (!stopping && p.closed_connections() >= 3) {
          print("sending SIGTERM after " + p.closed_connections().to_string() + " connections");
          p = p.signal(15);
          stopping = true;
        }
      }
    }
  }
  release(b, "/bin/srv", p, free);
  board.power_off(0);
  return 0;
}
`

// The program serves on port 80 until SIGTERM or SIGINT.
const netProcessServerSrc = `import "std/http";
import "std/platform";
import "std/serve";

function main(): i32 {
  let r = serve.run(80, serve.config(), (req: HttpRequest, host: platform.Host): HttpResponse => http.ok("hello from fern"));
  return serve.exit_code(r);
}
`

func TestSelfHostKernelServesFromAProcessOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	archive := processInitrd(t, cli, dir, map[string]string{"srv": netProcessServerSrc}, nil)
	src := writeTickProgram(t, processBoardSrc, netProcessMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", imageRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	port := freeTCPPort(t)
	args := []string{"-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "256M",
		"-nographic", "-monitor", "none", "-kernel", kernel, "-initrd", archive,
		"-global", "virtio-mmio.force-legacy=false",
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-10.0.2.15:80", port),
		"-device", "virtio-net-device,netdev=n0", "-device", "virtio-rng-device"}
	console, err := fetchHTTPThrough(t, qemu, port, args...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	for _, want := range []string{"sending SIGTERM after 3 connections\n", "/bin/srv exit 0\n", "/bin/srv gave back its frames true\n"} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
