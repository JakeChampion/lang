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
  let c0: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
  let ex: i32[] = pairs(d, 2000);
  print("exclusive listener " + (ex.len() == 2 && ex[0] == ln && ex[1] == 1).to_string());
  print("exclusive again " + d.watch(ln, 5).to_string());
  tcp_close(tcp_accept(ln));
  tcp_close(c0);
  d.watch(ln, 1);
  let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
  let got: i32[] = pairs(d, 2000);
  print("listener " + (got.len() == 2 && got[0] == ln && got[1] == 1).to_string());
  let a: i32 = tcp_accept(ln);
  d.watch(a, 1);
  tcp_send_bytes(c, [104u8, 101u8, 108u8, 108u8, 111u8]);
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
  tcp_send_bytes(c, [109u8, 111u8, 114u8, 101u8]);
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
    let ci: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
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
    tcp_send_bytes(ci, [120u8]);
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

// ringRecvProbe prints, over loopback, what a descriptor watched with
// interest 8 reads through recv_into (slice 3): a line read whole and then
// nothing reported; bytes left unread reported again and read in pieces
// smaller than they came, then EAGAIN; bytes received before the watch
// dropped bit 8 read first, ahead of what came after; the end of the
// stream; and forty connections that each sent 64 KiB before any was read,
// more than the ring's buffers hold, every byte of every stream read in
// order. Under epoll bit 8 is nothing and recv_into reads the socket, so
// the three legs print the same.
const ringRecvProbe = `import "std/async";
import "std/i32";

function pairs(d: async.RealDriver, ms: i32): i32[] {
  return d.wait(64, ms);
}

function text(buf: u8[], n: i32): string {
  if (n <= 0) {
    return n.to_string();
  }
  return string_from_bytes_unchecked(buf.take(n));
}

function pattern(seed: i32, n: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i + seed) % 251) as u8);
    i = i + 1;
  }
  return out;
}

function find(xs: i32[], x: i32): i32 {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == x) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function main(): i32 {
  let d: async.RealDriver = async.real_driver_io_uring();
  print("ring " + d.io_uring().to_string());
  let any: u8[] = [0u8, 0u8, 0u8, 0u8];
  let ln: i32 = tcp_listen_with(any, 0, 64, false);
  let port: i32 = tcp_local_port(ln);
  let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
  let a: i32 = tcp_accept(ln);
  let tries: i32 = 0;
  while (a < 0 && tries < 200) {
    sleep_ms(5 as i64);
    a = tcp_accept(ln);
    tries = tries + 1;
  }
  tcp_socket_ctl(a, 3, 1);
  d.watch(a, 9);
  let buf: u8[] = __alloc_u8(4096);
  tcp_send_bytes(c, [104u8, 101u8, 108u8, 108u8, 111u8]);
  let first: i32[] = pairs(d, 2000);
  print("readable " + (first.len() == 2 && first[0] == a && (first[1] & 1) == 1).to_string());
  print("read " + text(buf, d.recv_into(a, buf)) + " then " + pairs(d, 100).len().to_string());
  tcp_send_bytes(c, [97u8, 98u8, 99u8]);
  pairs(d, 2000);
  let again: i32[] = pairs(d, 100);
  print("unread reported again " + (again.len() == 2 && again[0] == a).to_string());
  let small: u8[] = [0u8, 0u8];
  print("pieces " + text(small, d.recv_into(a, small)) + " " + text(small, d.recv_into(a, small)) + " " + d.recv_into(a, small).to_string());
  tcp_send_bytes(c, [111u8, 110u8, 101u8]);
  pairs(d, 2000);
  d.watch(a, 1);
  tcp_send_bytes(c, [116u8, 119u8, 111u8]);
  sleep_ms(50 as i64);
  print("in order " + text(buf, d.recv_into(a, buf)));
  d.watch(a, 9);
  let forty: i32[] = [];
  let accs: i32[] = [];
  let i: i32 = 0;
  while (i < 40) {
    let ci: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
    let ai: i32 = tcp_accept(ln);
    let t: i32 = 0;
    while (ai < 0 && t < 200) {
      sleep_ms(5 as i64);
      ai = tcp_accept(ln);
      t = t + 1;
    }
    tcp_socket_ctl(ai, 3, 1);
    forty = forty.append(ci);
    accs = accs.append(ai);
    i = i + 1;
  }
  i = 0;
  while (i < 40) {
    tcp_send_bytes(forty[i], pattern(i, 32768));
    i = i + 1;
  }
  i = 0;
  while (i < 40) {
    d.watch(accs[i], 9);
    i = i + 1;
  }
  let got: i32[] = [];
  let bad: i32 = 0;
  i = 0;
  while (i < 40) {
    got = got.append(0);
    i = i + 1;
  }
  let whole: i32 = 0;
  let waits: i32 = 0;
  while (whole < 40 && waits < 2000) {
    let evs: i32[] = pairs(d, 2000);
    let j: i32 = 0;
    while (j * 2 < evs.len()) {
      let k: i32 = find(accs, evs[j * 2]);
      if (k >= 0) {
        let n: i32 = d.recv_into(accs[k], buf);
        while (n > 0) {
          let m: i32 = 0;
          while (m < n) {
            if (buf[m] != ((got[k] + m + k) % 251) as u8) {
              bad = bad + 1;
            }
            m = m + 1;
          }
          got = got.with(k, got[k] + n);
          if (got[k] == 32768) {
            whole = whole + 1;
          }
          n = d.recv_into(accs[k], buf);
        }
      }
      j = j + 1;
    }
    waits = waits + 1;
  }
  print("forty streams whole " + whole.to_string() + " out of order " + bad.to_string());
  tcp_socket_ctl(c, 4, 1);
  pairs(d, 2000);
  print("end of stream " + d.recv_into(a, buf).to_string());
  i = 0;
  while (i < 40) {
    d.unwatch(accs[i]);
    tcp_close(accs[i]);
    tcp_close(forty[i]);
    i = i + 1;
  }
  d.unwatch(a);
  tcp_close(a);
  tcp_close(c);
  tcp_close(ln);
  print("close " + d.close().to_string());
  return 0;
}
`

const ringRecvRest = "readable true\nread hello then 0\nunread reported again true\npieces ab c -11\nin order onetwo\nforty streams whole 40 out of order 0\nend of stream 0\nclose 0\n"

func runRingRecvProbe(t *testing.T, leg string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "ring_recv.fern")
	if err := os.WriteFile(src, []byte(ringRecvProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	ring := false
	switch leg {
	case "interp":
		cmd = exec.Command(buildLangBinForInterp(t), "-interp", src)
	case "x86-64-linux":
		if x86QemuOrEmpty(t) != "" {
			t.Skip("qemu-user has no io_uring; the x86-64 leg needs a native host")
		}
		out := filepath.Join(filepath.Dir(src), "ring_recv.bin")
		if o, err := exec.Command(buildFernCLI(t), "-target", leg, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build failed: %v\n%s", err, o)
		}
		cmd, ring = exec.Command(out), true
	case "arm64-linux":
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(filepath.Dir(src), "ring_recv.arm64")
		if o, err := exec.Command(buildFernCLI(t), "-target", leg, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build failed: %v\n%s", err, o)
		}
		cmd, ring = exec.Command(out), qemu == ""
		if qemu != "" {
			cmd = exec.Command(qemu, out)
		}
	}
	got, err := cmd.CombinedOutput()
	want := "ring " + map[bool]string{true: "true", false: "false"}[ring] + "\n" + ringRecvRest
	if err != nil || string(got) != want {
		t.Fatalf("probe: %v\n%s\nwant:\n%s", err, got, want)
	}
}

func TestRingRecvX86_64(t *testing.T) { runRingRecvProbe(t, "x86-64-linux") }
func TestRingRecvInterp(t *testing.T) { runRingRecvProbe(t, "interp") }
func TestRingRecvArm64(t *testing.T)  { runRingRecvProbe(t, "arm64-linux") }

// ringZcProbe prints what send_zc does (slice 4): a write of 32 KiB from
// offset 0 and one of its second half from offset 16,384, each reported
// writable once done, its result the bytes written, the peer reading them
// in order, and its slot notified and released. Every driver but an
// io_uring's answers -EOPNOTSUPP.
const ringZcProbe = `import "std/async";
import "std/i32";

function pattern(n: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, (i % 251) as u8);
    i = i + 1;
  }
  return out;
}

function read_back(c: i32, n: i32, from: i32): boolean {
  let buf: u8[] = __alloc_u8(n);
  let got: i32 = 0;
  while (got < n) {
    let piece: u8[] = __alloc_u8(n - got);
    let r: i32 = tcp_recv_into(c, piece);
    if (r <= 0) {
      return false;
    }
    let i: i32 = 0;
    while (i < r) {
      if (piece[i] != ((from + got + i) % 251) as u8) {
        return false;
      }
      i = i + 1;
    }
    got = got + r;
  }
  return true;
}

function send(d: async.RealDriver, a: i32, c: i32, data: u8[], from: i32): string {
  let s: i32 = d.send_zc(a, data, from);
  if (s < 0) {
    return "refused " + s.to_string();
  }
  let ev: i32[] = d.wait(64, 2000);
  let reported: boolean = ev.len() == 2 && ev[0] == a && (ev[1] & 2) == 2;
  let r: i32 = d.zc_result(s);
  let ok: boolean = read_back(c, r, from);
  let tries: i32 = 0;
  while (!d.zc_notified(s) && tries < 50) {
    d.wait(64, 100);
    tries = tries + 1;
  }
  return "reported " + reported.to_string() + " wrote " + r.to_string() + " read " + ok.to_string() + " notified " + d.zc_notified(s).to_string() + " released " + d.zc_release(s).to_string();
}

function main(): i32 {
  let d: async.RealDriver = async.real_driver_io_uring();
  print("ring " + d.io_uring().to_string());
  let any: u8[] = [0u8, 0u8, 0u8, 0u8];
  let ln: i32 = tcp_listen_with(any, 0, 64, false);
  let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], tcp_local_port(ln), false);
  let a: i32 = tcp_accept(ln);
  let tries: i32 = 0;
  while (a < 0 && tries < 200) {
    sleep_ms(5 as i64);
    a = tcp_accept(ln);
    tries = tries + 1;
  }
  tcp_socket_ctl(a, 3, 1);
  d.watch(a, 8);
  let data: u8[] = pattern(32768);
  print("whole " + send(d, a, c, data, 0));
  print("half " + send(d, a, c, data, 16384));
  d.unwatch(a);
  tcp_close(a);
  tcp_close(c);
  tcp_close(ln);
  print("close " + d.close().to_string());
  return 0;
}
`

func runRingZcProbe(t *testing.T, leg string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "ring_zc.fern")
	if err := os.WriteFile(src, []byte(ringZcProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	ring := false
	switch leg {
	case "interp":
		cmd = exec.Command(buildLangBinForInterp(t), "-interp", src)
	case "x86-64-linux":
		if x86QemuOrEmpty(t) != "" {
			t.Skip("qemu-user has no io_uring; the x86-64 leg needs a native host")
		}
		out := filepath.Join(filepath.Dir(src), "ring_zc.bin")
		if o, err := exec.Command(buildFernCLI(t), "-target", leg, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build failed: %v\n%s", err, o)
		}
		cmd, ring = exec.Command(out), true
	case "arm64-linux":
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(filepath.Dir(src), "ring_zc.arm64")
		if o, err := exec.Command(buildFernCLI(t), "-target", leg, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("build failed: %v\n%s", err, o)
		}
		cmd, ring = exec.Command(out), qemu == ""
		if qemu != "" {
			cmd = exec.Command(qemu, out)
		}
	}
	got, err := cmd.CombinedOutput()
	want := "ring false\nwhole refused -95\nhalf refused -95\nclose 0\n"
	if ring {
		want = "ring true\nwhole reported true wrote 32768 read true notified true released 0\nhalf reported true wrote 16384 read true notified true released 0\nclose 0\n"
	}
	if err != nil || string(got) != want {
		t.Fatalf("probe: %v\n%s\nwant:\n%s", err, got, want)
	}
}

func TestRingZcX86_64(t *testing.T) { runRingZcProbe(t, "x86-64-linux") }
func TestRingZcInterp(t *testing.T) { runRingZcProbe(t, "interp") }
func TestRingZcArm64(t *testing.T)  { runRingZcProbe(t, "arm64-linux") }
