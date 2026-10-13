// async.real_driver_io_uring() (docs/NET-P7-IO-URING-PLAN.md slice 1): the
// io_uring readiness set behind the Driver reports what epoll does. One
// probe runs natively on x86-64, where the ring is had, and in the
// interpreter and under qemu-aarch64, where it is not and the driver keeps
// the reactor; past the first line the three print the same.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ringDriverProbe prints, over loopback: a listener watched exclusively
// reported on a connect, and a second exclusive watch of it refused as
// epoll refuses one; the listener reported on a connect when watched
// plainly; an accepted socket reported while its bytes stay unread and not
// once they are read, as level-triggered epoll reports it; writable once
// asked for; nothing after an unwatch, and readable again once watched
// anew, with a completion of the poll the unwatch removed not reported; a
// peer's half-close reported readable and not hung up, as epoll reports it
// to a loop that never asks for EPOLLRDHUP; and
// forty connections each reported readable in one pass of waits.
const ringDriverProbe = `import "std/async";
import "std/i32";

function pairs(d: async.RealDriver, ms: i32): i32[] {
  return d.wait(64, ms);
}

function main(): i32 {
  let d: async.RealDriver = async.real_driver_io_uring();
  print("ring " + d.io_uring().to_string());
  let any: u8[] = [0u8, 0u8, 0u8, 0u8];
  let ln: i32 = tcp_listen_with(any, 0, 64, false);
  let port: i32 = tcp_local_port(ln);
  d.watch(ln, 5);
  let c0: i32 = tcp_connect(16777343, port);
  let ex: i32[] = pairs(d, 2000);
  print("exclusive listener " + (ex.len() == 2 && ex[0] == ln && ex[1] == 1).to_string());
  print("exclusive again " + d.watch(ln, 5).to_string());
  tcp_close(tcp_accept(ln));
  tcp_close(c0);
  d.watch(ln, 1);
  let c: i32 = tcp_connect(16777343, port);
  let got: i32[] = pairs(d, 2000);
  print("listener " + (got.len() == 2 && got[0] == ln && got[1] == 1).to_string());
  let a: i32 = tcp_accept(ln);
  d.watch(a, 1);
  tcp_send(c, "hello");
  let first: i32[] = pairs(d, 2000);
  print("readable " + (first.len() == 2 && first[0] == a && (first[1] & 1) == 1).to_string());
  let again: i32[] = pairs(d, 100);
  print("unread reported again " + (again.len() == 2 && again[0] == a).to_string());
  let buf: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
  let n: i32 = tcp_recv_into(a, buf);
  print("read " + n.to_string() + " then " + pairs(d, 100).len().to_string());
  d.watch(a, 3);
  let w: i32[] = pairs(d, 2000);
  print("writable " + (w.len() == 2 && w[0] == a && (w[1] & 2) == 2).to_string());
  d.unwatch(a);
  tcp_send(c, "more");
  print("unwatched " + pairs(d, 100).len().to_string());
  d.watch(a, 1);
  let back: i32[] = pairs(d, 2000);
  print("watched anew " + (back.len() == 2 && back[0] == a).to_string());
  tcp_recv_into(a, buf);
  print("drained " + pairs(d, 100).len().to_string());
  tcp_socket_ctl(c, 4, 1);
  let half: i32[] = pairs(d, 2000);
  print("half-closed readable, not hung up " + (half.len() == 2 && half[0] == a && half[1] == 1).to_string());
  print("end of stream " + tcp_recv_into(a, buf).to_string());
  d.unwatch(a);
  let cs: i32[] = [];
  let accs: i32[] = [];
  let i: i32 = 0;
  while (i < 40) {
    let ci: i32 = tcp_connect(16777343, port);
    let ai: i32 = tcp_accept(ln);
    let tries: i32 = 0;
    while (ai < 0 && tries < 200) {
      sleep_ms(5 as i64);
      ai = tcp_accept(ln);
      tries = tries + 1;
    }
    cs = cs.append(ci);
    accs = accs.append(ai);
    d.watch(ai, 1);
    tcp_send(ci, "x");
    i = i + 1;
  }
  let seen: i32[] = [];
  let waits: i32 = 0;
  while (seen.len() < 40 && waits < 50) {
    let evs: i32[] = pairs(d, 2000);
    let j: i32 = 0;
    while (j * 2 < evs.len()) {
      let fd: i32 = evs[j * 2];
      if (fd != ln && !seen.contains(fd)) {
        seen = seen.append(fd);
        tcp_recv_into(fd, buf);
      }
      j = j + 1;
    }
    waits = waits + 1;
  }
  print("forty " + seen.len().to_string());
  i = 0;
  while (i < 40) {
    d.unwatch(accs[i]);
    tcp_close(accs[i]);
    tcp_close(cs[i]);
    i = i + 1;
  }
  d.unwatch(ln);
  tcp_close(c);
  tcp_close(a);
  tcp_close(ln);
  print("close " + d.close().to_string());
  return 0;
}
`

const ringDriverRest = "exclusive listener true\nexclusive again -22\nlistener true\nreadable true\nunread reported again true\nread 5 then 0\nwritable true\nunwatched 0\nwatched anew true\ndrained 0\nhalf-closed readable, not hung up true\nend of stream 0\nforty 40\nclose 0\n"

func writeRingDriverProbe(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "ring_probe.fern")
	if err := os.WriteFile(src, []byte(ringDriverProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func checkRingDriver(t *testing.T, out []byte, err error, ring bool) {
	t.Helper()
	want := "ring false\n" + ringDriverRest
	if ring {
		want = "ring true\n" + ringDriverRest
	}
	if err != nil || string(out) != want {
		t.Fatalf("probe: %v\n%s\nwant:\n%s", err, out, want)
	}
}

// The ring is had natively here (kernel 6.1 or later outside a seccomp
// profile refusing it, which the CI runners are); a host without one is
// a failure to investigate, not a skip.
func TestRingDriverX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	if qemu != "" {
		t.Skip("qemu-user has no io_uring; the x86-64 leg needs a native host")
	}
	src := writeRingDriverProbe(t)
	out := filepath.Join(filepath.Dir(src), "ring_probe.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	got, err := runX86Bin(qemu, out).CombinedOutput()
	checkRingDriver(t, got, err, true)
}

func TestRingDriverInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := writeRingDriverProbe(t)
	got, err := exec.Command(bin, "-interp", src).CombinedOutput()
	checkRingDriver(t, got, err, false)
}

// Under qemu-aarch64 there is no io_uring, so this is the fallback's leg;
// on a native arm64 host the ring is had and the probe reads as x86-64's.
func TestRingDriverArm64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	src := writeRingDriverProbe(t)
	out := filepath.Join(filepath.Dir(src), "ring_probe.arm64")
	if o, err := exec.Command(bin, "-target", "arm64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	cmd := exec.Command(out)
	if qemu != "" {
		cmd = exec.Command(qemu, out)
	}
	got, err := cmd.CombinedOutput()
	checkRingDriver(t, got, err, qemu == "")
}
