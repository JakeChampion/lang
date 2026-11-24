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
  if (n != 100 as i64) {
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

// The record is read above a match, and only one arm uses it, across a call
// and a join. The read moves down into that arm. The plan then takes the
// record, its array and the element there, and stores each back at the join
// (#14440). Each Bytes round allocates only its array.
const sinkToUsesProg = `import "std/array";

enum RBody { Text(string), Bytes(u8[]) }
struct Resp { status: i32, body: RBody, background: Option[i64] }
struct Session { buf: u8[], sent: i64 }
struct Conn { n: i64, date: string, log: u8[], session: Session }
struct Q { conns: Conn[], k: i32, producers: i64[], name: string }
struct Conns { quic: Q, date: string, serial: i32, later: i64[] }

function (r: Resp) body_bytes(): u8[] {
  match (r.body) {
    Text(s) => { return s.bytes(); },
    Bytes(b) => { return b; }
  }
}

@noinline
function (c: Conn) with_date(d: string): Conn {
  if (d == c.date) { return c; }
  return Conn { ...c, date: d };
}

function (s: Session) send(bytes: [u8], whole: boolean): Session {
  let out = s.buf;
  for b in bytes {
    out = out.append(b);
  }
  if (whole) {
    return Session { ...s, buf: out, sent: s.sent + 1 };
  }
  return Session { ...s, buf: out };
}

function (c: Conn) __respond_with(id: i64, bytes: [u8]): Conn {
  let n = bytes.len();
  if (id < 0) {
    n = 0;
  }
  let sent = Conn { ...c, n: c.n + id + n as i64, session: c.session.send(bytes[0:n], id % 3 == 0) };
  if (id > 100000) {
    return sent;
  }
  return Conn { ...sent, n: sent.n + 1 };
}

function respond(c: Conn, id: i64, resp: Resp): Conn {
  match (resp.body) {
    Text(s) => { return c.__respond_with(id, s.as_bytes()); },
    _ => { return c.__respond_with(id, resp.body_bytes()); }
  }
}

function stage(own c: Conns, r: Resp): Conns {
  match (r.background) {
    Some(w) => {
      return Conns { ...c, later: c.later.append(w) };
    },
    None => {
      return c;
    }
  }
}

@noinline
function h3_respond(own c: Conns, i: i32, id: i64, resp: Resp): Conns {
  if (i < 0) { return c; }
  let cur = stage(c, resp);
  let q: Q = cur.quic;
  match (resp.body) {
    Bytes(b) => {
      if (b.len() == 77) {
        return Conns { ...cur, quic: Q { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date)) } };
      }
      return Conns { ...cur, quic: Q { ...q, conns: q.conns.with(i, q.conns[i].with_date(cur.date)) } };
    },
    _ => {
      return Conns { ...cur, quic: Q { ...q, conns: q.conns.with(i, respond(q.conns[i].with_date(cur.date), id, resp)) } };
    }
  }
}

function main(): i32 {
  let conns: Conn[] = [];
  for i in 0..4 {
    conns = conns.append(Conn { n: i as i64, date: "", log: [], session: Session { buf: [], sent: 0 } });
  }
  let c: Conns = Conns { quic: Q { conns: conns, k: 0, producers: [], name: "q" }, date: "today", serial: 7, later: [] };
  let before = __heap_alloc_count();
  for round in 0..1000 {
    let body: RBody = if (round % 2 == 0) { Text("hello") } else { Bytes([1 as u8, 2 as u8]) };
    c = h3_respond(c, round % 4, round as i64, Resp { status: 200, body: body, background: None });
  }
  let n = __heap_alloc_count() - before;
  if (c.quic.conns[0].n != 126000 as i64 || c.quic.conns[0].session.buf.len() != 1250) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n != 1024 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostSinkReadToItsUses(t *testing.T) {
	runRereadProgram(t, "sink_to_uses.fern", sinkToUsesProg)
}

// A string two records down is compared before an update of both records.
// The comparison only reads the string, so the update block reads the
// records again and takes them. Before, the comparison counted as a spend of
// the string: the first reads stayed, and each round copied both records.
const compareThenTakeProg = `struct St { list: i64[], n: i64, refused: string }
struct Q { streams: St, k: i64 }
struct Conn { quic: Q, parked: i64[] }

@noinline
function readable(own c: Conn): Conn {
  if (c.quic.streams.refused != "") {
    return c;
  }
  return Conn { ...c, quic: Q { ...c.quic, streams: St { ...c.quic.streams, n: c.quic.streams.n + 1 } } };
}

function main(): i32 {
  let c = Conn { quic: Q { streams: St { list: [], n: 0, refused: "" }, k: 0 }, parked: [] };
  let before = __heap_alloc_count();
  for i in 0..1000 {
    c = readable(c);
  }
  let n = __heap_alloc_count() - before;
  if (c.quic.streams.n != 1000 as i64) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n > 10 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostCompareThenTake(t *testing.T) {
	runRereadProgram(t, "compare_then_take.fern", compareThenTakeProg)
}
