package e2eselfhost

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
  var ws: Words = s.words;
  s = State { ...s, words: s.spare };
  var lines: string[] = ws.lines;
  var nl: i32 = ws.nl;
  var pos: i64[] = ws.pos;
  var meta: i64[] = ws.meta;
  var nw: i32 = ws.n;
  var tat: i32[] = ws.at;
  var tbest: i64[] = ws.best;
  var li: i32 = 0 - 1;
  var i: i32 = 0;
  while (i < line.len()) {
    if (nw > 100000) {
      var c: Words = chunk(Words { lines: lines, nl: nl, pos: pos, meta: meta, n: nw, at: tat, best: tbest }, nw);
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
  var o: i32 = s.o + 1;
  return scan(State { ...s, o: o }, line);
}
function empty(): Words { return Words { lines: [], nl: 0, pos: [], meta: [], n: 0, at: [], best: [] }; }
function main(): i32 {
  var s: State = State { words: empty(), spare: empty(), bytes: 0, o: 0 };
  var k: i32 = 0;
  while (k < 50) { s = feed(s, "abcdefgh"); k = k + 1; }
  var a1: i64 = __heap_alloc_count();
  while (k < 150) { s = feed(s, "abcdefgh"); k = k + 1; }
  var a2: i64 = __heap_alloc_count();
  if (s.words.n != 1200 || s.words.pos[1199] != 7 as i64 || s.words.lines.len() != 150) { return 100; }
  return ((a2 - a1) / 100 as i64) as i32;
}
`

// The fields are taken after a loop and read across later blocks, where the
// flow from before the take keeps the record live: the record's drop must
// still come, past the slots the takes emptied. Built with the leak census.
const recordFieldTakeAcrossProg = `struct Pair { a: i32[], b: i32[] }
@noinline
function left(own p: Pair, x: i32): Pair {
    var a: i32[] = p.a;
    var b: i32[] = p.b;
    return Pair { a: a.with(0, x), b: b };
}
function main(): i32 {
    var p: Pair = Pair { a: [1, 2], b: [5, 6] };
    var i: i32 = 0;
    while (i < 20) { p = left(p, i); i = i + 1; }
    var a: i32[] = p.a;
    var b: i32[] = p.b;
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
  var a: i32[] = r.a;
  var m: i32 = r.n;
  var b: B = keep(a);
  return b.xs.len() + a[0] + m;
}
@noinline
function cost(a: i64[], b: i64[], s: i32): i64 {
  return a[s] + b[s];
}
@noinline
function step(own w: W, n: i32): W {
  var a: i64[] = w.a;
  var b: i64[] = w.b;
  var c: i64[] = w.c;
  var m: i32 = w.n;
  var s: i32 = n - 1;
  while (s >= 0) {
    c = c.with(s, cost(a, b, s));
    s = s - 1;
  }
  return W { a: a, b: b, c: c, n: m };
}
function main(): i32 {
  var xs: i32[] = [];
  var i: i32 = 0;
  while (i < 5) { xs = xs.append(i + 10); i = i + 1; }
  var got: i32 = f(R { a: xs, n: 1 });
  var w: W = W { a: [1 as i64, 2 as i64, 3 as i64], b: [4 as i64, 5 as i64, 6 as i64], c: [0 as i64, 0 as i64, 0 as i64], n: 3 };
  var k: i32 = 0;
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
