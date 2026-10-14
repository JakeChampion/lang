package e2ecompiler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const zeroCopySource = `import "std/http";
import "std/platform";
import "std/serve";
import "core/int";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  let n: i32 = 0;
  match (int.parse_int_radix(req.path.drop(1), 10)) {
    Some(v) => {
      n = v;
    },
    None => {}
  }
  let body: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    body = body.with(i, (i % 251) as u8);
    i = i + 1;
  }
  return http.bytes(200, body).with_header("x-size", req.path);
}

function main(): i32 {
  return serve.run(0, serve.config(), handle);
}
`

// TestSelfHostServeZeroCopy answers bytes bodies across the zero-copy
// threshold (Config.io_uring_zc_bytes, 3072): 1,000 bytes, exactly 3,072,
// 64 KiB, 1 MiB and 8 MiB, on one kept connection, on connections the
// client closes after each response, to HTTP/1.0, to HEAD, and to sixteen
// clients at once. Every body must come back whole under its head. Under
// epoll the bodies are copied; under the ring the large ones go out by
// IORING_OP_SEND_ZC, in as many writes as the socket takes, each body held
// until the kernel's notification.
func TestSelfHostServeZeroCopy(t *testing.T)     { runZeroCopy(t, false) }
func TestSelfHostServeZeroCopyRing(t *testing.T) { runZeroCopy(t, true) }

func runZeroCopy(t *testing.T, ring bool) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(zeroCopySource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1"))
	if ring {
		cmd.Env = append(os.Environ(), "FERN_IO_URING=1")
	}
	addr, stderr := e2eharness.StartInheritedServer(t, cmd)
	fail := func(format string, args ...any) {
		t.Helper()
		got, _ := os.ReadFile(stderr)
		t.Fatalf(format+"\nserver stderr:\n%s\n%s", append(args, got, ringState(cmd.Process.Pid))...)
	}
	sizes := []int{1000, 3072, 65536, 1 << 20, 8 << 20}
	kept := &http.Client{Timeout: 60 * time.Second}
	closing := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for _, c := range []*http.Client{kept, closing} {
		for _, n := range sizes {
			if err := getPattern(c, addr, n); err != nil {
				fail("%v", err)
			}
		}
	}
	for _, n := range sizes {
		if err := http10Pattern(addr, n); err != nil {
			fail("%v", err)
		}
		resp, err := kept.Head("http://" + addr + "/" + strconv.Itoa(n))
		if err != nil || resp.StatusCode != 200 || resp.ContentLength != int64(n) {
			fail("HEAD /%d: %v %v", n, resp, err)
		}
		resp.Body.Close()
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &http.Client{Timeout: 120 * time.Second}
			for _, n := range []int{8 << 20, 65536, 1 << 20, 3072} {
				if err := getPattern(c, addr, n+i); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		fail("%v", err)
	}
	if ring {
		if err := ringSubmitted(cmd.Process.Pid); err != nil {
			fail("%v", err)
		}
	}
}

// ringSubmitted waits up to two seconds for the idle server's rings to
// hold no submission the kernel was not given: SqHead equal to SqTail.
func ringSubmitted(pgid int) error {
	var last string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		last = ""
		for _, info := range ringInfos(pgid) {
			head, tail := fdinfoField(info, "SqHead"), fdinfoField(info, "SqTail")
			if head != tail {
				last = fmt.Sprintf("SqHead %s, SqTail %s", head, tail)
			}
		}
		if last == "" {
			return nil
		}
	}
	return fmt.Errorf("the idle server's ring holds submissions the kernel was not given: %s", last)
}

func fdinfoField(info, key string) string {
	for _, line := range strings.Split(info, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func checkPattern(body []byte, n int) error {
	if len(body) != n {
		return fmt.Errorf("/%d: %d bytes", n, len(body))
	}
	for i, b := range body {
		if int(b) != i%251 {
			return fmt.Errorf("/%d: byte %d is %d", n, i, b)
		}
	}
	return nil
}

func getPattern(c *http.Client, addr string, n int) error {
	resp, err := c.Get("http://" + addr + "/" + strconv.Itoa(n))
	if err != nil {
		return fmt.Errorf("GET /%d: %v", n, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("x-size") != "/"+strconv.Itoa(n) {
		return fmt.Errorf("GET /%d: %d %q after %d bytes: %v", n, resp.StatusCode, resp.Header.Get("x-size"), len(body), err)
	}
	return checkPattern(body, n)
}

// ringState is what the kernel says of the server's process group: its
// release, and for each process its wait channel and every io_uring it
// holds, by fdinfo (the rings' heads and tails, and their overflow list).
func ringState(pgid int) string {
	var b strings.Builder
	rel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	fmt.Fprintf(&b, "kernel %s", rel)
	for _, proc := range groupProcs(pgid) {
		wchan, _ := os.ReadFile(filepath.Join(proc, "wchan"))
		fmt.Fprintf(&b, "%s wchan %s\n", proc, wchan)
		for fd, info := range procRings(proc) {
			fmt.Fprintf(&b, "%s:\n%s\n", fd, info)
		}
	}
	return b.String()
}

// ringInfos is the fdinfo of every io_uring the process group holds.
func ringInfos(pgid int) []string {
	var infos []string
	for _, proc := range groupProcs(pgid) {
		for _, info := range procRings(proc) {
			infos = append(infos, info)
		}
	}
	return infos
}

func groupProcs(pgid int) []string {
	var group []string
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, proc := range procs {
		stat, err := os.ReadFile(filepath.Join(proc, "stat"))
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if err == nil && len(fields) >= 3 && fields[2] == strconv.Itoa(pgid) {
			group = append(group, proc)
		}
	}
	return group
}

// procRings maps each io_uring descriptor of the process to its fdinfo.
func procRings(proc string) map[string]string {
	rings := map[string]string{}
	fds, _ := filepath.Glob(filepath.Join(proc, "fd", "*"))
	for _, fd := range fds {
		if link, _ := os.Readlink(fd); link == "anon_inode:[io_uring]" {
			info, _ := os.ReadFile(filepath.Join(proc, "fdinfo", filepath.Base(fd)))
			rings[fd] = string(info)
		}
	}
	return rings
}

// http10Pattern asks over HTTP/1.0, which the server answers and closes.
func http10Pattern(addr string, n int) error {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	fmt.Fprintf(c, "GET /%d HTTP/1.0\r\n\r\n", n)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return fmt.Errorf("HTTP/1.0 /%d: %v", n, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.HasPrefix(resp.Proto, "HTTP/1.") {
		return fmt.Errorf("HTTP/1.0 /%d: %v", n, err)
	}
	return checkPattern(body, n)
}
