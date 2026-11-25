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

// rereadTwinKeptProg reads s.list in the entry block, and the record update
// reads it again as cur.list. A spliced helper then puts a join between them.
// The early sink must not read s.list[i].id again in the branch: cur.list is
// live there, so a third read of the slot keeps the plan from taking the
// array, and the last update copies the list and boxes the element (#14440).
func TestSelfHostRereadKeepsTheTwinRead(t *testing.T) {
	runRereadProgram(t, "reread_twin_kept.fern", rereadTwinKeptProg)
}

const rereadTwinKeptProg = `import "std/array";

struct Params { bidi: i64, uni: i64 }
struct St { id: i64, closed: boolean, buf: u8[] }
struct Ss { list: St[], closed: i64[], limits: i64[], dirty: boolean[], local: Params, retired: i32 }

function initial(s: Ss, t: i32): i64 {
  if ((t & 2) == 0) {
    return s.local.bidi;
  }
  return s.local.uni;
}

function (st: St) close_it(): St {
  return St { ...st, closed: true };
}

@noinline
function retire(s: Ss, i: i32): Ss {
  let st = s.list[i];
  if (st.closed) {
    return s;
  }
  let cur = Ss { ...s, retired: s.retired + 1 };
  if ((st.id & 1) == 1) {
    let t = (st.id & 3) as i32;
    let init = initial(cur, t);
    let target = cur.closed[t] + 1 + init;
    cur = Ss { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (target - cur.limits[t] >= init / 8) {
      cur = Ss { ...cur, limits: cur.limits.with(t, target), dirty: cur.dirty.with(t, true) };
    }
  }
  cur = Ss { ...cur, list: cur.list.with(i, cur.list[i].close_it()) };
  return cur;
}

function main(): i32 {
  let list: St[] = [];
  for k in 0..4 {
    list = list.append(St { id: k as i64, closed: false, buf: [] });
  }
  let s = Ss { list: list, closed: [0, 0, 0, 0], limits: [0, 0, 0, 0], dirty: [false, false, false, false], local: Params { bidi: 100, uni: 3 }, retired: 0 };
  let before = __heap_alloc_count();
  for round in 0..1000 {
    let i = round % 4;
    s = Ss { ...s, list: s.list.with(i, St { ...s.list[i], closed: false }) };
    s = retire(s, i);
  }
  let n = __heap_alloc_count() - before;
  if (s.retired != 1000 || s.closed[1] != 250 || s.closed[3] != 250 || s.closed[0] != 0 || !s.dirty[3] || s.limits[3] != 253) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n > 100 as i64) {
    return 2;
  }
  return 0;
}
`

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

// An element is lent to a call before a spliced update with a join, and is
// stored back after the join. The call only borrows the element, so the
// update block reads the array again and takes the element there (#14440).
const lentThenTakeAcrossJoinProg = `import "std/array";

struct Stream { buf: u8[], base: i64, sent: i64, fin_sent: boolean }
struct Streams { list: Stream[], filled: u8[], send_sent: i64, next: i32 }

@noinline
function written(st: Stream): i64 {
  return st.base + st.buf.len() as i64;
}

@noinline
function append_range(out: u8[], b: [u8], from: i32, to: i32): u8[] {
  let o = out;
  for k in from..to {
    o = o.append(b[k]);
  }
  return o;
}

function sent_of(st: Stream, n: i64, fin: boolean): Stream {
  return Stream { ...st, sent: st.sent + n, fin_sent: st.fin_sent || fin };
}

@noinline
function new_data(s: Streams, i: i32, room: i32): Streams {
  let sent = s.list[i].sent;
  let w = written(s.list[i]);
  let n = w - sent;
  if (room as i64 < n) {
    n = room as i64;
  }
  if (n == 0) {
    return s;
  }
  let at = sent as i32;
  let out = append_range(s.filled, s.list[i].buf, at, at + n as i32);
  return Streams { ...s, filled: out, list: s.list.with(i, sent_of(s.list[i], n, sent + n == w)) };
}

function main(): i32 {
  let list: Stream[] = [];
  for j in 0..4 {
    let b: u8[] = [];
    for q in 0..(8000 + j / 3 * 12000) {
      b = b.append(q as u8);
    }
    list = list.append(Stream { buf: b, base: 0, sent: 0, fin_sent: false });
  }
  let s = Streams { list: list, filled: [], send_sent: 0, next: 0 };
  let before = __heap_alloc_count();
  for round in 0..1000 {
    s = Streams { ...new_data(s, round % 4, 40), filled: [] };
  }
  let n = __heap_alloc_count() - before;
  if (s.list[0].sent != 8000 as i64 || !s.list[0].fin_sent || s.list[3].sent != 10000 as i64 || s.list[3].fin_sent) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n > 1100 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostLentThenTakeAcrossJoin(t *testing.T) {
	runRereadProgram(t, "lent_then_take_across_join.fern", lentThenTakeAcrossJoinProg)
}

// A spliced call chain takes a payload out of an array element and updates
// four nested records of it. The payload lives past the block that stores
// the element back, and the last update spends it. So that block reads the
// element again, and the plan reuses the box of the element and of each
// record (#14440).
const splicedChainTakesTheElementProg = `import "std/array";
import "std/result";

struct P { key: u8[], seq: u64, suite: i32 }
struct Conn { write: P, other: u8[], quic: boolean }
struct Server { conn: Conn, state: i32, names: u8[] }
struct Session { server: Server, tag: u8[] }
enum Tls { Plain, Secured(Session) }
struct Conns { tls: Tls[], socks: i32[], out: u8[] }
struct Sealed { record: u8[], next: P }
struct Wrote { conn: Conn, bytes: u8[] }
struct SStep { server: Server, output: u8[] }
struct Step { session: Session, output: u8[] }

@noinline
function seal_in_place(p: P, own buf: u8[]): u8[] {
  return buf.with(0, p.key[0]);
}

function advanced(p: P): P {
  return P { ...p, seq: p.seq + 1u64 };
}

function seal(p: P, own buf: u8[]): u8[] {
  if (p.suite == 1) {
    return seal_in_place(p, buf);
  }
  return seal_in_place(p, buf.with(1, 7u8));
}

function seal_framed(p: P, own buf: u8[]): Result[Sealed, i32] {
  if (p.seq == 0xffffffffffffffffu64) {
    return Err(1);
  }
  let r = seal(p, buf);
  return Ok(Sealed { record: r, next: advanced(p) });
}

function cwrite(c: Conn, own buf: u8[]): Result[Wrote, i32] {
  if (c.quic) {
    return Err(2);
  }
  match (seal_framed(c.write, buf)) {
    Ok(s) => {
      return Ok(Wrote { conn: Conn { ...c, write: s.next }, bytes: s.record });
    },
    Err(e) => {
      return Err(e);
    }
  }
}

function swrite(s: Server, own buf: u8[]): Result[SStep, i32] {
  if (s.state != 1) {
    return Err(3);
  }
  let w = cwrite(s.conn, buf)?;
  return Ok(SStep { server: Server { ...s, conn: w.conn }, output: w.bytes });
}

function sess_write(s: Session, own buf: u8[]): Result[Step, i32] {
  let st = swrite(s.server, buf)?;
  return Ok(Step { session: Session { ...s, server: st.server }, output: st.output });
}

@noinline
function send_out(own c: Conns, at: i32, own framed: u8[]): Conns {
  match (c.tls[at]) {
    Secured(sess) => {
      c = Conns { ...c, tls: c.tls.with(at, Plain) };
      match (sess_write(sess, framed)) {
        Ok(step) => {
          return Conns { ...c, tls: c.tls.with(at, Secured(step.session)), out: step.output };
        },
        Err(_) => {
          return c;
        }
      }
    },
    _ => {}
  }
  return c;
}

function main(): i32 {
  let p = P { key: [9u8, 8u8], seq: 0u64, suite: 1 };
  let sess = Session { server: Server { conn: Conn { write: p, other: [1u8], quic: false }, state: 1, names: [2u8] }, tag: [3u8] };
  let c = Conns { tls: [Plain, Secured(sess)], socks: [1, 2], out: [] };
  let before = __heap_alloc_count();
  for r in 0..1000 {
    let framed: u8[] = [0u8, 0u8, 0u8];
    c = send_out(c, 1, framed);
  }
  let n = __heap_alloc_count() - before;
  let seq: u64 = 0u64;
  match (c.tls[1]) {
    Secured(s) => { seq = s.server.conn.write.seq; },
    _ => {}
  }
  if (seq != 1000u64 || c.out[0] != 9u8) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  if (n > 1100 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostSplicedChainTakesTheElement(t *testing.T) {
	runRereadProgram(t, "spliced_chain_takes_the_element.fern", splicedChainTakesTheElementProg)
}
