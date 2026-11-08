package e2ecompiler

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The fifth slice of the network stack (#14259): one handler runs under the
// hosted std/serve on this machine and under std/tcp/serve on a kernel on
// qemu, with the device's IRQ line as its wake. The test sends the same
// requests to both and compares the answers: the status, the headers but the
// date, and the body. The handler echoes the method, the path and the body.
const kernelServeHandlerSrc = `function answer(req: HttpRequest): HttpResponse {
  return http.ok("method=" + req.method + " path=" + req.path + " body=" + req.body_text_lossy());
}
`

const kernelServeHostedSrc = `import "std/http";
import "std/platform";
import "std/serve";

` + kernelServeHandlerSrc + `
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
  return answer(req);
}

function main(): i32 {
  return serve.exit_code(serve.run(PORT, serve.config(), handle));
}
`

// kernelServeExchanges is the number of requests the transcript sends to a
// server, the refused one included.
const kernelServeExchanges = 8

const kernelServeImports = `import "std/async";
import "std/http";
import "std/tcp";
import "std/tcp/serve";
`

// The kernel stops after the readiness probe and the scripted requests.
var kernelServeRun = `  let srv = serve.new(tcp.listen(iface, 80, 1000 as u32), answer);
  print("serving on tcp 80");
  while (srv.answered() < ` + fmt.Sprint(kernelServeExchanges+1) + ` || srv.active() > 0) {
    let Some(woke) = async.block_on(async.race([async.irq(NET_LINE), async.irq(board.TIMER_IRQ)])) else {
      print("cancelled");
      return 1;
    };
    let (which, line) = woke;
    if (line == NET_LINE) {
      let used = n.interrupt();
    }
    srv = srv.step(board.irq_count());
  }
  print("served " + srv.answered().to_string());`

var kernelServeMainSrc = kernelNetMain(kernelServeImports, kernelServeHandlerSrc+kernelIRQLineArm64Src, `  let NET_LINE = net_line(t, d.base);
  if (NET_LINE < 32) {
    print("no interrupt line for the device");
    return 1;
  }
  board.gic_init();
  board.gic_enable(NET_LINE);
  board.timer_start();
  __irq_unmask();
`+kernelServeRun)

var kernelServeX86MainSrc = kernelNetX86Main(kernelServeImports, kernelServeHandlerSrc+kernelIRQLineX86Src, `  let gsi = net_gsi(cmdline, d.base);
  if (gsi < 0) {
    print("no interrupt line for the device");
    return 1;
  }
  let NET_LINE = board.NET_IRQ;
  board.apic_init();
  board.ioapic_route(gsi);
  __irq_unmask();
`+kernelServeRun)

// exchange sends one raw request on a new connection and returns the
// normalized responses it reads back: `count` of them, as HTTP/1.1 parses
// them, with the date header dropped.
func exchange(t *testing.T, addr string, raw string, count int) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "dial: " + err.Error()
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, raw); err != nil {
		return "write: " + err.Error()
	}
	return readResponses(t, bufio.NewReader(conn), count)
}

// expectContinue sends a request head with Expect: 100-continue, reads the
// interim response, sends the body, and returns both responses normalized.
func expectContinue(t *testing.T, addr string, path string, body string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "dial: " + err.Error()
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", path, len(body))
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		return "interim: " + err.Error()
	}
	blank, _ := r.ReadString('\n')
	out := "interim " + strings.TrimSpace(line) + " " + strings.TrimSpace(blank) + "\n"
	io.WriteString(conn, body)
	return out + readResponses(t, r, 1)
}

func readResponses(t *testing.T, r *bufio.Reader, count int) string {
	t.Helper()
	var out strings.Builder
	for i := 0; i < count; i++ {
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			fmt.Fprintf(&out, "response %d: %v\n", i, err)
			return out.String()
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Fprintf(&out, "%s\n", resp.Status)
		names := make([]string, 0, len(resp.Header))
		for name := range resp.Header {
			if !strings.EqualFold(name, "Date") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&out, "%s: %s\n", name, strings.Join(resp.Header[name], ", "))
		}
		if resp.Close {
			out.WriteString("close\n")
		}
		fmt.Fprintf(&out, "body %q\n", body)
	}
	return out.String()
}

// transcript runs the scripted requests against addr: a GET, a POST with a
// body, a HEAD, two pipelined requests on one connection, an HTTP/1.0
// request, a request that expects 100-continue, and a request line that no
// parser frames.
func transcript(t *testing.T, addr string) string {
	t.Helper()
	var out strings.Builder
	out.WriteString(exchange(t, addr, "GET /hello?x=1 HTTP/1.1\r\nHost: x\r\n\r\n", 1))
	out.WriteString(exchange(t, addr, "POST /echo HTTP/1.1\r\nHost: x\r\nContent-Length: 11\r\n\r\nhello world", 1))
	out.WriteString(exchange(t, addr, "HEAD /hello HTTP/1.1\r\nHost: x\r\n\r\n", 1))
	out.WriteString(exchange(t, addr, "GET /a HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", 2))
	out.WriteString(exchange(t, addr, "GET /old HTTP/1.0\r\n\r\n", 1))
	out.WriteString(expectContinue(t, addr, "/continue", "later"))
	out.WriteString(exchange(t, addr, "GARBAGE\r\n\r\n", 1))
	return out.String()
}

// waitServing retries a GET until the server at addr answers.
func waitServing(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for attempt := 0; attempt < 100; attempt++ {
		resp, err := client.Get("http://" + addr + "/ready")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s never answered", addr)
}

// hostedTranscript builds the std/serve program for this machine, runs it on
// a free port, and returns the transcript of the scripted requests.
func hostedTranscript(t *testing.T, cli *selfHostCLI) string {
	t.Helper()
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "hosted.fern")
	if err := os.WriteFile(src, []byte(strings.Replace(kernelServeHostedSrc, "PORT", fmt.Sprint(port), 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "hosted")
	if msg, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build hosted: %v\n%s", err, msg)
	}
	cmd := exec.Command(bin)
	var console bytes.Buffer
	cmd.Stdout = &console
	cmd.Stderr = &console
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServing(t, addr)
	got := transcript(t, addr)
	if strings.Contains(got, "dial:") || strings.Contains(got, "response 0:") {
		t.Fatalf("hosted transcript:\n%s\nconsole:\n%s", got, console.Bytes())
	}
	return got
}

// kernelTranscript boots the kernel image with the host port forwarded to
// its port 80, runs the scripted requests, and returns the transcript and
// the console once the VM stops.
func kernelTranscript(t *testing.T, qemu string, port int, args ...string) (string, []byte, error) {
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
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServing(t, addr)
	got := transcript(t, addr)
	err := cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not stop in 120s; transcript:\n%s\nconsole:\n%s", got, console.Bytes())
	}
	return got, console.Bytes(), err
}

func checkKernelServeMatches(t *testing.T, hosted, kernel string, console []byte) {
	t.Helper()
	if hosted != kernel {
		t.Errorf("the kernel's answers differ from std/serve's\n--- std/serve\n%s\n--- std/tcp/serve\n%s\nconsole:\n%s", hosted, kernel, console)
	}
	for _, want := range []string{"serving on tcp 80\n", fmt.Sprintf("served %d\n", kernelServeExchanges+1)} {
		if !strings.Contains(string(console), want) {
			t.Errorf("console:\n%s\nwant %q", console, want)
		}
	}
}

func TestSelfHostKernelServeMatchesHostedOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	hosted := hostedTranscript(t, cli)
	src := writeTickProgram(t, dmaBoardSrc, kernelServeMainSrc)
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
	kernel, console, err := kernelTranscript(t, qemu, port, args...)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	checkKernelServeMatches(t, hosted, kernel, console)
}

// main returns 16, so qemu exits with 33 from isa-debug-exit.
func TestSelfHostKernelX86ServeMatchesHostedOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	hosted := hostedTranscript(t, cli)
	src := writeTickProgram(t, dmaX86BoardSrc, kernelServeX86MainSrc)
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
	kernel, console, err := kernelTranscript(t, qemu, port, args...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	checkKernelServeMatches(t, hosted, kernel, console)
}
