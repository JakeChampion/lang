package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The flight is read off the connection before a borrowing call, and the
// connection is stored back before the flight's loop. Each flush allocates
// only the transmitted datagram (#14739).
const rereadPastSpendProg = `struct Cfg { ecn: boolean, name: string }

struct Q { config: Cfg, out: u8[][], failed: boolean, a: i64[], b: i64[], c: string }

@noinline function (c: Q) __marking(): boolean {
  return c.config.ecn && !c.failed;
}

function (c: Q) codepoint(): i32 {
  if (c.__marking()) {
    return 2;
  }
  return 0;
}

struct Outer { quic: Q, session: i64[], started: boolean }

struct Quic { conns: Outer[], paths: u8[][], out: u8[][] }

struct Holder { quic: Quic, other: i64[] }

function (c: Outer) codepoint(): i32 {
  return c.quic.codepoint();
}

function (c: Outer) datagrams(): u8[][] {
  return c.quic.out;
}

function (c: Outer) sent(): Outer {
  return Outer { ...c, quic: Q { ...c.quic, out: [] } };
}

function (c: Outer) transmit(): Outer {
  return Outer { ...c, quic: Q { ...c.quic, out: [[3u8]] } };
}

function added(out: u8[][], flight: u8[][], mark: i32): u8[][] {
  let o = out;
  for d in flight {
    o = o.append(d);
  }
  if (mark > 5) {
    o = [];
  }
  return o;
}

function flush(own c: Holder, i: i32): Holder {
  let qq = c.quic;
  let built = Holder { ...c, quic: Quic { ...qq, conns: qq.conns.with(i, qq.conns[i].transmit()) } };
  let b = built.quic;
  let flight = b.conns[i].datagrams();
  let mark = b.conns[i].codepoint();
  return Holder { ...built, quic: Quic { ...b, conns: b.conns.with(i, b.conns[i].sent()), out: added(b.out, flight, mark) } };
}

function main(): i32 {
  let cs: Outer[] = [];
  for k in 0..8 {
    cs = cs.append(Outer { quic: Q { config: Cfg { ecn: true, name: "x" }, out: [], failed: false, a: [1i64], b: [2i64], c: "c" }, session: [1i64], started: true });
  }
  let h = Holder { quic: Quic { conns: cs, paths: [], out: [] }, other: [] };
  for r in 0..8 {
    h = flush(h, r);
    h = Holder { ...h, quic: Quic { ...h.quic, out: [] } };
  }
  let before = __heap_alloc_count();
  for r in 0..100 {
    h = flush(h, r % 8);
    if (h.quic.out.len() != 1 || h.quic.out[0][0] != 3u8 || h.quic.conns[r % 8].quic.out.len() != 0) {
      return 1;
    }
    h = Holder { ...h, quic: Quic { ...h.quic, out: [] } };
  }
  let n = __heap_alloc_count() - before;
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n != 200 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostRereadPastAncestorSpend(t *testing.T) {
	runRereadProgram(t, "reread_past_spend.fern", rereadPastSpendProg)
}

// The element and its flight are read before a borrowing call, and the
// element is stored back after it. The re-read below the call must not take
// a new read of the record while the array read above the call stays live:
// each flush allocates only the transmitted datagram.
const rereadChildKeptProg = `struct Cfg { ecn: boolean, name: string }

struct Q { config: Cfg, out: u8[][], failed: boolean, a: i64[], b: i64[], c: string }

@noinline function (c: Q) __marking(): boolean {
  return c.config.ecn && !c.failed;
}

function (c: Q) codepoint(): i32 {
  if (c.__marking()) {
    return 2;
  }
  return 0;
}

struct Outer { quic: Q, session: i64[], started: boolean }

struct Quic { conns: Outer[], paths: u8[][], out: u8[][] }

struct Holder { quic: Quic, other: i64[] }

function (c: Outer) codepoint(): i32 {
  return c.quic.codepoint();
}

function (c: Outer) datagrams(): u8[][] {
  return c.quic.out;
}

function (c: Outer) sent(): Outer {
  return Outer { ...c, quic: Q { ...c.quic, out: [] } };
}

function (c: Outer) transmit(): Outer {
  return Outer { ...c, quic: Q { ...c.quic, out: [[3u8]] } };
}

@noinline function added(out: u8[][], to: u8[], flight: u8[][], mark: i32): u8[][] {
  let o = out;
  for d in flight {
    o = o.append(d);
  }
  if (mark > 5) {
    o = [];
  }
  return o;
}

function flush(own c: Holder, i: i32): Holder {
  let qq = c.quic;
  let built = Holder { ...c, quic: Quic { ...qq, conns: qq.conns.with(i, qq.conns[i].transmit()) } };
  let b = built.quic;
  let flight = b.conns[i].datagrams();
  let mark = b.conns[i].codepoint();
  return Holder { ...built, quic: Quic { ...b, conns: b.conns.with(i, b.conns[i].sent()), out: added(b.out, b.paths[i], flight, mark) } };
}

function main(): i32 {
  let cs: Outer[] = [];
  for k in 0..8 {
    cs = cs.append(Outer { quic: Q { config: Cfg { ecn: true, name: "x" }, out: [], failed: false, a: [1i64], b: [2i64], c: "c" }, session: [1i64], started: true });
  }
  let ps: u8[][] = [];
  for k in 0..8 {
    ps = ps.append([1u8]);
  }
  let h = Holder { quic: Quic { conns: cs, paths: ps, out: [] }, other: [] };
  for r in 0..8 {
    h = flush(h, r);
    h = Holder { ...h, quic: Quic { ...h.quic, out: [] } };
  }
  let before = __heap_alloc_count();
  for r in 0..100 {
    h = flush(h, r % 8);
    if (h.quic.out.len() != 1 || h.quic.out[0][0] != 3u8 || h.quic.conns[r % 8].quic.out.len() != 0) {
      return 1;
    }
    h = Holder { ...h, quic: Quic { ...h.quic, out: [] } };
  }
  let n = __heap_alloc_count() - before;
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n != 100 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostRereadKeepsTheChildRead(t *testing.T) {
	runRereadProgram(t, "reread_child_kept.fern", rereadChildKeptProg)
}

func runRereadProgram(t *testing.T, name, prog string) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, name)
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a value came back wrong, 2: a flush copied the connection, 99: a count went under)", tg.target, got)
		}
	}
}
