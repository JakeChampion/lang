package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The three shapes the serve path stores: an Instant field, an Instant field
// of records kept in an array, and an Option[Duration] field. Each is held in
// lanes, so the loop allocates only the two records it builds per iteration.
const inlineFieldsProg = `import "std/bench";
import "std/time";

struct Sent { pn: i64, at: Instant, size: i32 }

struct Wait { task: i32, set: i32[], timeout: Option[Duration] }

struct Kept { fd: i32, since: Instant }

@noinline function sent(pn: i64, now: i64): Sent {
  return Sent { pn: pn, at: Instant { sec: now / 1000, nsec: (now % 1000) as i32 }, size: 1200 };
}

@noinline function age(s: Sent, now: Instant): i64 {
  return now.since(s.at).as_nanos();
}

@noinline function waited(task: i32, ms: i32): Wait {
  let t: Option[Duration] = None;
  if (ms >= 0) {
    t = Some(time.duration_millis(ms as i64));
  }
  return Wait { task: task, set: [], timeout: t };
}

@noinline function due(w: Wait): i64 {
  let Some(t) = w.timeout else {
    return 0 - 1;
  };
  return t.as_nanos();
}

@noinline function moved(w: Wait): Wait {
  return Wait { ...w, task: w.task + 1 };
}

function main(): i32 {
  let now: Instant = Instant { sec: 50, nsec: 7 };
  let kept: Kept[] = [];
  let total: i64 = 0;
  let a0: i64 = bench.alloc_count();
  let i: i64 = 0;
  while (i < 100) {
    let s: Sent = sent(i, i * 3);
    total = total + age(s, now) / 1000 + s.size as i64;
    let w: Wait = moved(waited(i as i32, (i % 3 - 1) as i32));
    total = total + due(w) / 1000 + w.task as i64;
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  i = 0;
  while (i < 4) {
    kept = kept.append(Kept { fd: i as i32, since: Instant { sec: i, nsec: 0 } });
    i = i + 1;
  }
  for k in kept {
    total = total + now.since(k.since).as_millis() + k.fd as i64;
  }
  print(total.to_string() + " " + allocated.to_string());
  if (total != 5000351959 as i64) {
    return 1;
  }
  if (allocated != 200) {
    return 2;
  }
  return 0;
}
`

// Every read path of a field held in lanes: destructuring, a record update
// that keeps it, a closure that captures the record, and derived equality
// and display on a record whose Duration field is held in lanes.
const inlinePathsProg = `import "std/time";
import "core/cmp";

@derive(cmp.Eq, cmp.Display)
struct Lap { n: i32, took: Duration }

struct Sent { pn: i64, at: Instant, size: i32 }

@noinline function sent(pn: i64, now: i64): Sent {
  return Sent { pn: pn, at: Instant { sec: now, nsec: 5 }, size: 1200 };
}

@noinline function resized(s: Sent, size: i32): Sent {
  return Sent { ...s, size: size };
}

@noinline function apart(s: Sent): i64 {
  let Sent { pn, at, size } = s;
  return pn + at.sec + at.nsec as i64 + size as i64;
}

@noinline function captured(s: Sent): () => i64 {
  return () => s.at.sec * 2;
}

function main(): i32 {
  let total: i64 = 0;
  let shown: string = "";
  let i: i64 = 0;
  while (i < 10) {
    let s: Sent = resized(sent(i, i * 7), (i * 3) as i32);
    let f: () => i64 = captured(s);
    total = total + apart(s) + f();
    let a: Lap = Lap { n: i as i32, took: time.duration_millis(i * 11) };
    let b: Lap = Lap { n: i as i32, took: time.duration_millis(i * 11) };
    if (a == b) {
      total = total + 1;
    }
    if (i == 3) {
      shown = a.to_string();
    }
    i = i + 1;
  }
  print(shown + " " + total.to_string());
  if (shown != "Lap { n: 3, took: 33ms }") {
    return 1;
  }
  if (total != 1185) {
    return 2;
  }
  return 0;
}
`

// Fields that must stay boxed: a read kept past the record, a destructured
// field kept, a read handed to a function value, a read boxed as a trait
// object, and a record with a finalizer.
const inlineBoxedProg = `import "core/mem";
import "core/cmp";
import "std/time";

struct Stored { n: i32, at: Instant }

struct Split { n: i32, at: Instant }

struct Called { n: i32, at: Instant }

struct Shown { n: i32, took: Duration }

struct Guarded { n: i32, at: Instant, p: usize }

impl mem.Drop for Guarded {
  function drop(self: Self): void {
    __store_i64(self.p, __load_i64(self.p) + 1);
  }
}

@noinline function stored(s: Stored, into: Instant[]): Instant[] {
  return into.append(s.at);
}

@noinline function split(s: Split, into: Instant[]): Instant[] {
  let Split { n, at } = s;
  return into.append(at);
}

@noinline function secs(i: Instant): i64 {
  return i.sec;
}

@noinline function called(s: Called): i64 {
  let g: (Instant) => i64 = secs;
  return g(s.at);
}

@noinline function shown(s: Shown): string {
  let d: dyn cmp.Display = s.took;
  return d.to_string();
}

@noinline function guarded(s: Guarded): i64 {
  return s.at.sec + s.n as i64;
}

function main(): i32 {
  let counter: i64[] = __alloc_i64(1);
  let p: usize = (counter as usize) + 8;
  __store_i64(p, 0);
  let kept: Instant[] = [];
  let total: i64 = 0;
  let i: i64 = 0;
  while (i < 5) {
    kept = stored(Stored { n: 1, at: Instant { sec: i, nsec: 1 } }, kept);
    kept = split(Split { n: 2, at: Instant { sec: i * 2, nsec: 2 } }, kept);
    total = total + called(Called { n: 3, at: Instant { sec: i * 3, nsec: 3 } });
    total = total + guarded(Guarded { n: 4, at: Instant { sec: i * 4, nsec: 4 }, p: p });
    i = i + 1;
  }
  for k in kept {
    total = total + k.sec + k.nsec as i64;
  }
  let text: string = shown(Shown { n: 5, took: time.duration_millis(1500) });
  print(text + " " + total.to_string() + " " + __load_i64(p).to_string());
  if (text != "1s500ms" || total != 135) {
    return 1;
  }
  if (__load_i64(p) != 5 || counter.len() != 1) {
    return 2;
  }
  return 0;
}
`

// A record an export carries keeps its declared layout; the same record
// without the export has its Instant field in lanes.
const inlineExportedProg = `struct Mark { at: Instant, n: i32 }

@export("t:p/api", "measure")
function measure(s: Mark): i64 {
  return s.at.sec + s.n as i64;
}

@noinline function inner(s: Mark): i64 {
  return s.at.sec * 2;
}

function main(): i32 {
  let s: Mark = Mark { at: Instant { sec: 4, nsec: 0 }, n: 3 };
  print((inner(s) + measure(s)).to_string());
  return 0;
}
`

// The stdlib's own records: recovery.Sent's Instant and async.Wait's
// Option[Duration], read in a program that builds both.
const inlineStdlibProg = `import "std/quic/recovery";
import "std/async";
import "std/time";

@noinline function stamp(s: recovery.Sent): i64 {
  return s.at.sec;
}

@noinline function limit(w: async.Wait): i64 {
  let Some(t) = w.timeout else {
    return 0 - 1;
  };
  return t.as_millis();
}

function main(): i32 {
  let s: recovery.Sent = recovery.Sent { pn: 1, at: Instant { sec: 9, nsec: 0 }, size: 1200, ack_eliciting: true, in_flight: true, resend: [] };
  let r: recovery.Recovery = recovery.new(1200, true).on_sent(recovery.APPLICATION, s);
  let w: async.Wait = async.Wait { task: 1, set: [], timeout: Some(time.duration_millis(40)) };
  print((stamp(s) + limit(w) + r.pto(recovery.INITIAL).as_millis()).to_string());
  return 0;
}
`

func TestSelfHostInlineField(t *testing.T) {
	cli := buildSelfHostCLI(t)
	progs := []struct{ name, src string }{
		{"fields", inlineFieldsProg},
		{"paths", inlinePathsProg},
		{"boxed", inlineBoxedProg},
	}
	for _, prog := range progs {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(prog.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, prog.src, target, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
				if code != 0 {
					t.Errorf("exit %d, want 0 (1 or 2: a wrong value or count)\n%s", code, stderr)
				}
				if !strings.Contains(stderr, "live_bytes=0") {
					t.Errorf("%q, want every block freed", stderr)
				}
			})
		}
	}
}

// semDump compiles src for x86-64 and returns the semantic graph of fn as the
// inliner leaves it.
func semDump(t *testing.T, cli *selfHostCLI, src, fn string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(dir, "main.s"), path, cli.stdlib)
	cmd.Env = childEnv("FERN_SEM_DUMP=" + fn)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v: %s", err, out)
	}
	return string(out)
}

func TestSelfHostInlineFieldLayout(t *testing.T) {
	cli := buildSelfHostCLI(t)
	laneRead := func(field string) *regexp.Regexp {
		return regexp.MustCompile(`(?m) k-6 \[\d+,\] imm=\d+ ` + regexp.QuoteMeta(field) + `\$`)
	}
	wholeRead := func(field string) *regexp.Regexp {
		return regexp.MustCompile(`(?m) k-6 \[\d+,\] imm=\d+ ` + regexp.QuoteMeta(field) + `$`)
	}
	cases := []struct {
		name, src, fn, field string
		laned                bool
	}{
		{"destructured-apart", inlinePathsProg, "apart", "at", true},
		{"update-keeps-lanes", inlinePathsProg, "resized", "at", true},
		{"read-kept-whole", inlineBoxedProg, "stored", "at", false},
		{"destructured-kept-whole", inlineBoxedProg, "split", "at", false},
		{"handed-to-function-value", inlineBoxedProg, "called", "at", false},
		{"boxed-as-trait-object", inlineBoxedProg, "shown", "took", false},
		{"finalizer", inlineBoxedProg, "guarded", "at", false},
		{"exported", inlineExportedProg, "inner", "at", false},
		{"not-exported", strings.Replace(inlineExportedProg, `@export("t:p/api", "measure")`, "", 1), "inner", "at", true},
		{"recovery-sent", inlineStdlibProg, "stamp", "at", true},
		{"async-wait", inlineStdlibProg, "limit", "timeout", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dump := semDump(t, cli, c.src, c.fn)
			if !strings.Contains(dump, "FUNC "+c.fn+" ") {
				t.Fatalf("no dump of %s:\n%s", c.fn, dump)
			}
			laned, whole := laneRead(c.field).MatchString(dump), wholeRead(c.field).MatchString(dump)
			if c.laned && (!laned || whole) {
				t.Errorf("%s reads %s whole, want its lanes:\n%s", c.fn, c.field, dump)
			}
			if !c.laned && (laned || !whole) {
				t.Errorf("%s reads %s in lanes, want the boxed field:\n%s", c.fn, c.field, dump)
			}
		})
	}
}
