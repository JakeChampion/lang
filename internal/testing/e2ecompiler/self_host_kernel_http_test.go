package e2ecompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The third slice of the network stack (#14259): the image answers HTTP
// requests on port 80 over std/tcp, with std/http reading the request and
// writing the response. qemu's user network forwards a host port to it, so
// the test fetches from the host with Go's client. The retransmission timers
// of the connections run on the board's timer ticks.
const kernelHTTPServeSrc = `function push_all(data: u8[], more: u8[]): u8[] {
  for b in more {
    data = data.append(b);
  }
  return data;
}

// Answers three requests, each on its own connection, then stops once every
// connection is gone.
function serve(l: tcp.Listener): string {
  let pending: u8[][] = [[], [], [], [], [], [], [], []];
  let answered = 0;
  let turns = 0;
  while (turns < 400000000 && (answered < 3 || l.active() > 0)) {
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
    turns += 1;
  }
  return "http requests " + answered.to_string();
}
`

const kernelHTTPImports = `import "std/http";
import "std/tcp";
`

var kernelHTTPMainSrc = kernelNetMain(kernelHTTPImports, kernelHTTPServeSrc, `  board.gic_init();
  board.timer_start();
  __irq_unmask();
  let l = tcp.listen(iface, 80, 1000 as u32);
  print("listening on tcp 80");
  print(serve(l));`)

var kernelHTTPX86MainSrc = kernelNetX86Main(kernelHTTPImports, kernelHTTPServeSrc, `  board.apic_init();
  __irq_unmask();
  let l = tcp.listen(iface, 80, 1000 as u32);
  print("listening on tcp 80");
  print(serve(l));`)

var kernelHTTPWantLines = []string{"listening on tcp 80\n", "request GET /hello1\n", "request GET /hello3\n", "http requests 3\n"}

// fetchHTTPThrough boots the VM with the host port forwarded to the guest's TCP
// port 80, fetches three paths until each answer arrives, and returns the
// console and qemu's exit error. Before the guest listens, the host's
// connection is refused or the request waits on a SYN that gets no answer, so
// each fetch is tried again with a short timeout.
func fetchHTTPThrough(t *testing.T, qemu string, port int, args ...string) ([]byte, error) {
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
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for n := 1; n <= 3; n++ {
		got := ""
		for attempt := 0; attempt < 100 && got != "hello from fern"; attempt++ {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/hello%d", port, n))
			if err != nil {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil && resp.StatusCode == http.StatusOK {
				got = string(body)
			}
		}
		if got != "hello from fern" {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("request %d never got its answer, last body %q; console:\n%s", n, got, console.Bytes())
		}
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not stop in 120s; console:\n%s", console.Bytes())
	}
	return console.Bytes(), err
}

func TestSelfHostKernelHTTPOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, kernelHTTPMainSrc)
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
	for _, want := range kernelHTTPWantLines {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

// main returns 16, so qemu exits with 33 from isa-debug-exit.
func TestSelfHostKernelX86HTTPOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, kernelHTTPX86MainSrc)
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
	for _, want := range kernelHTTPWantLines {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}
