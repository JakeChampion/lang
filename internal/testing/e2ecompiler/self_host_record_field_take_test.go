package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A record field read moves out of a record this frame owns when the record
// dies at the read and is read after it only through its other fields
// (ssaunits.payload_root), and a read of a field of a record it moved out
// takes in turn. `scan` is fmt's scan_body in miniature: the words are read
// out of the state, and out of the words their arrays, which the loop then
// updates in place. Read as borrows, each array held a second count through
// the words record, and the first update in every call copied it.
//
// The exit code is the allocations per call: the Words the call returns.
const recordFieldTakeProg = `struct Words { lines: string[], nl: i32, pos: i64[], meta: i64[], n: i32, at: i32[], best: i64[] }
struct State { words: Words, spare: Words, bytes: i32, o: i32 }
@noinline function chunk(own w: Words, k: i32): Words {
  return Words { ...w, n: 0, nl: 0 };
}
@noinline function scan(own s: State, line: string): State {
  let ws: Words = s.words;
  s = State { ...s, words: s.spare };
  let lines: string[] = ws.lines;
  let nl: i32 = ws.nl;
  let pos: i64[] = ws.pos;
  let meta: i64[] = ws.meta;
  let nw: i32 = ws.n;
  let tat: i32[] = ws.at;
  let tbest: i64[] = ws.best;
  let li: i32 = 0 - 1;
  let i: i32 = 0;
  while (i < line.len()) {
    if (nw > 100000) {
      let c: Words = chunk(Words { lines: lines, nl: nl, pos: pos, meta: meta, n: nw, at: tat, best: tbest }, nw);
      lines = c.lines;
      nl = c.nl;
      pos = c.pos;
      meta = c.meta;
      nw = c.n;
      tat = c.at;
      tbest = c.best;
    }
    if (li < 0) {
      li = nl;
      if (nl < lines.len()) { lines = lines.with(nl, line); } else { lines = lines.append(line); }
      nl = nl + 1;
    }
    if (nw < pos.len()) {
      pos = pos.with(nw, i as i64);
      meta = meta.with(nw, 1 as i64);
    } else {
      pos = pos.append(i as i64);
      meta = meta.append(1 as i64);
    }
    nw = nw + 1;
    i = i + 1;
  }
  return State { ...s, words: Words { lines: lines, nl: nl, pos: pos, meta: meta, n: nw, at: tat, best: tbest } };
}
@noinline function feed(own s: State, line: string): State {
  let o: i32 = s.o + 1;
  return scan(State { ...s, o: o }, line);
}
function empty(): Words { return Words { lines: [], nl: 0, pos: [], meta: [], n: 0, at: [], best: [] }; }
function main(): i32 {
  let s: State = State { words: empty(), spare: empty(), bytes: 0, o: 0 };
  let k: i32 = 0;
  while (k < 50) { s = feed(s, "abcdefgh"); k = k + 1; }
  let a1: i64 = __heap_alloc_count();
  while (k < 150) { s = feed(s, "abcdefgh"); k = k + 1; }
  let a2: i64 = __heap_alloc_count();
  if (s.words.n != 1200 || s.words.pos[1199] != 7 as i64 || s.words.lines.len() != 150) { return 100; }
  return ((a2 - a1) / 100 as i64) as i32;
}
`

// The fields are taken after a loop and read across later blocks, where the
// flow from before the take keeps the record live: the record's drop must
// still come, past the slots the takes emptied. Built with the leak census.
// `id` keeps each array literal off the static data a constant one is.
const recordFieldTakeAcrossProg = `struct Pair { a: i32[], b: i32[] }
@noinline function id(x: i32): i32 { return x; }
@noinline
function left(own p: Pair, x: i32): Pair {
    let a: i32[] = p.a;
    let b: i32[] = p.b;
    return Pair { a: a.with(0, x), b: b };
}
function main(): i32 {
    let p: Pair = Pair { a: [id(1), 2], b: [id(5), 6] };
    let i: i32 = 0;
    while (i < 20) { p = left(p, i); i = i + 1; }
    let a: i32[] = p.a;
    let b: i32[] = p.b;
    if (a[0] != 19 || a[1] != 2 || b[0] != 5 || b[1] != 6) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

func TestSelfHostRecordFieldTakeAcrossBlocks(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "across.fern")
	if err := os.WriteFile(src, []byte(recordFieldTakeAcrossProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a field came back wrong, 99: a count went under)", tg.target, got)
		}
		if !strings.Contains(stderr.String(), "allocs=3 frees=3 live_bytes=0") {
			t.Errorf("%s: %q, want every one of the three blocks freed once", tg.target, stderr.String())
		}
	}
}

// A projection a call or instruction takes may leave a record that later
// blocks read only through its other fields (ssaunits.projection_root). In
// `step` the spliced `kept` branches inside the update, so `c` is read after
// the branch, through its other fields alone: `a` and `b` are taken and
// updated in place, where they were copied every call. `reread` reads the
// taken field again in a later block and `again` reaches the take again round
// a loop, so both keep the field the record's.
//
// The exit code: 1 for a wrong value, 2 for an allocation in the update loop,
// 99 for a count that went under.
const withTakeAcrossBlocksProg = `import "std/bench";

struct F { n: i32, s: string }
struct C { a: i32[], b: i32[], n: i32, last: F }

function kept(f: F): F {
  if (f.s.len() == 0) {
    return f;
  }
  return F { ...f, s: "" };
}

@noinline function step(c: C, at: i32, f: F): C {
  return C { ...c, a: c.a.with(at, c.a[at] + f.n), b: c.b.with(at, 1), last: kept(f) };
}

@noinline function reread(c: C, at: i32, flag: boolean): i32 {
  let w: i32[] = c.a.with(at, 100);
  let s: i32 = 0;
  if (flag) {
    s = c.a[at];
  }
  return w[at] + s + c.n;
}

@noinline function again(c: C): i32 {
  let t: i32 = 0;
  let i: i32 = 0;
  while (i < 3) {
    let w: i32[] = c.a.with(0, i);
    t = t + w[0] + c.a[0];
    i = i + 1;
  }
  return t;
}

function eight(): i32[] {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 8) {
    xs = xs.append(i);
    i = i + 1;
  }
  return xs;
}

function main(): i32 {
  let f: F = F { n: 2, s: "" };
  let c: C = C { a: eight(), b: eight(), n: 3, last: f };
  let a0: i64 = bench.alloc_count();
  let i: i32 = 0;
  while (i < 100) {
    c = step(c, i % 8, f);
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  if (c.a[5] != 29 || c.b[5] != 1 || reread(c, 3, true) != 132 || again(c) != 81) {
    return 1;
  }
  if (a1 != a0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

func TestSelfHostWithTakeAcrossBlocks(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "with_take.fern")
	if err := os.WriteFile(src, []byte(withTakeAcrossBlocksProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a value came back wrong, 2: the update copied, 99: a count went under)", tg.target, got)
		}
		if !strings.Contains(stderr.String(), "live_bytes=0") {
			t.Errorf("%s: %q, want every block freed", tg.target, stderr.String())
		}
	}
}

// A record updated in one arm of a branch reaches the join beside the
// unchanged record of the arms that leave it alone, and the join's phi reads
// the old record only on those arms' edges. In `joined` the field `grow`
// rebuilds is taken at its read: the phi's read of the old record is not on
// the path from the take, so `grow`'s loop appends in place. Read as live,
// the record kept a second count on the field and the first append of every
// call copied it. In `kept` the arm that skips the update hands the join the
// record the field was read from, so that field stays the record's.
//
// The exit code: 1 for a wrong value, 2 for an allocation per call in
// `joined`, 99 for a count that went under.
const recordFieldTakeArmJoinProg = `struct A { xs: i32[], n: i32, tag: string }
@noinline function bump(own a: A): A { return A { ...a, n: a.n + 1 }; }
function grow(own buf: i32[], line: string): i32[] {
  let i: i32 = 0;
  while (i < line.len()) {
    if (line[i] == b'"') {
      return buf;
    }
    buf = buf.append(line[i] as i32);
    i = i + 1;
  }
  return buf;
}
@noinline function joined(own a: A, lines: string[], rounds: i32): A {
  let j: i32 = 0;
  while (j < rounds) {
    let line: string = lines[j % 3];
    if (line.len() == 2) {
      a = bump(a);
    } else if (line != "skip") {
      a = A { ...a, xs: grow(a.xs, line) };
    }
    j = j + 1;
  }
  return a;
}
function grown(buf: i32[], line: string): i32[] {
  return buf.append(line[0] as i32);
}
@noinline function kept(own a: A, line: string, rounds: i32): A {
  let j: i32 = 0;
  while (j < rounds) {
    let xs: i32[] = a.xs;
    if (j % 2 == 0) {
      a = A { ...a, xs: grown(xs, line) };
    }
    j = j + 1;
  }
  return a;
}
function main(): i32 {
  let lines: string[] = ["abc\"", "de", "skip"];
  let c0: i64 = __heap_alloc_count();
  let a: A = joined(A { xs: [], n: 0, tag: "t" }, lines, 3000);
  let c1: i64 = __heap_alloc_count();
  if (a.xs.len() != 3000 || a.xs[2999] != 99 || a.n != 1000 || a.tag != "t") {
    return 1;
  }
  let b: A = kept(A { xs: [], n: 0, tag: "u" }, "x", 40);
  if (b.xs.len() != 20 || b.xs[19] != 120 || b.n != 0 || b.tag != "u") {
    return 1;
  }
  if (c1 - c0 > 32 as i64) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

func TestSelfHostRecordFieldTakeAtArmJoin(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "arm_join.fern")
	if err := os.WriteFile(src, []byte(recordFieldTakeArmJoinProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a value came back wrong, 2: the update copied, 99: a count went under)", tg.target, got)
		}
		if !strings.Contains(stderr.String(), "live_bytes=0") {
			t.Errorf("%s: %q, want every block freed", tg.target, stderr.String())
		}
	}
}

func TestSelfHostRecordFieldTake(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "take.fern")
	if err := os.WriteFile(src, []byte(recordFieldTakeProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 1 {
			t.Errorf("%s: exit %d, want 1 allocation per call (100: the words came back wrong; 2: an array was copied)", tg.target, got)
		}
	}
}

// A field that took its unit is the frame's own from then on, and its record
// may already be freed. `keep` counts its operand and `a` is read after it,
// so the call is handed a retained unit; `cost` only reads its operands, so
// the loop calls it with no retain. Treated as a field of `r` or `w`, the
// operand was gated on the dead record's count: `keep` took `a`'s only unit
// and `f` read it after the free, and `step` retained and released both
// arrays round every call. Built with the sanitizer, which quarantines a
// freed block.
const recordFieldTakeHandedProg = `struct R { a: i32[], n: i32 }
struct B { xs: i32[] }
struct W { a: i64[], b: i64[], c: i64[], n: i32 }
@noinline
function keep(xs: i32[]): B {
  return B { xs: xs };
}
@noinline
function f(own r: R): i32 {
  let a: i32[] = r.a;
  let m: i32 = r.n;
  let b: B = keep(a);
  return b.xs.len() + a[0] + m;
}
@noinline
function cost(a: i64[], b: i64[], s: i32): i64 {
  return a[s] + b[s];
}
@noinline
function step(own w: W, n: i32): W {
  let a: i64[] = w.a;
  let b: i64[] = w.b;
  let c: i64[] = w.c;
  let m: i32 = w.n;
  let s: i32 = n - 1;
  while (s >= 0) {
    c = c.with(s, cost(a, b, s));
    s = s - 1;
  }
  return W { a: a, b: b, c: c, n: m };
}
function main(): i32 {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 5) { xs = xs.append(i + 10); i = i + 1; }
  let got: i32 = f(R { a: xs, n: 1 });
  let w: W = W { a: [1 as i64, 2 as i64, 3 as i64], b: [4 as i64, 5 as i64, 6 as i64], c: [0 as i64, 0 as i64, 0 as i64], n: 3 };
  let k: i32 = 0;
  while (k < 3) { w = step(w, 3); k = k + 1; }
  if (__rc_underflow_count() != 0) { return 99; }
  return got + (w.c[2] as i32);
}
`

func TestSelfHostRecordFieldTakeHandedOn(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "handed.fern")
	if err := os.WriteFile(src, []byte(recordFieldTakeHandedProg), 0o644); err != nil {
		t.Fatal(err)
	}
	stepBody := regexp.MustCompile(`(?s)\n__fn_step\.r:\n(.*?)\n__fn_`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		m := stepBody.FindSubmatch(asm)
		if m == nil {
			t.Fatalf("%s: __fn_step.r not found in the listing", target)
		}
		if strings.Contains(string(m[1]), "__fern_rc_inc") {
			t.Errorf("%s: step retains a taken array round its call to cost:\n%s", target, m[1])
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_SANITIZE=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 25 {
			t.Errorf("%s: exit %d, want 25 (124: a freed block was touched; 99: a count went under)\n%s", tg.target, got, stderr.String())
		}
	}
}
