package e2eselfhost

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// #8678: coreutils/seq threads an output block through its stepping loop and
// hands it through a tuple-returning helper —
//
//	var d: (BufWriter, Block) = drain(w, out); w = d.0; out = d.1;
//
// — and every generation of that block leaked under the self-host, 64 KiB per
// term, until the OOM killer took the process. Each shape below pins one of
// the things that refused or prevented the local's rebind release:
//
//   - `return (w, out)` read as a container-literal escape of `out`, where
//     `return T { f: out }` and `return g(out)` were already admitted as the
//     box going back inside the result. The same reading left `flush`'s
//     `return (o, b)` marking its PARAMETER non-consume-safe, which refused
//     every caller passing a threaded local to it.
//   - the handback pair `var d = g(.., out, ..); out = d.f;` was recognised
//     only when the unpack was the very next statement; seq reads `w = d.0`
//     in between, a differently-typed element that cannot be the box.
//   - `return (o, b)` retained the bare parameter for the tuple, so the
//     caller received its own box with a count nobody gave back; the next
//     rebind found it shared and left its fields — the buffer — to a dead
//     tuple. A returned struct param is an uncounted alias by the Return
//     lowering's own rule, and the tuple literal now follows it.
//   - a call in statement position taking the local, `overflow(w, out)`,
//     read as an escape however consume-safe the callee — and its own
//     `finish(flush(w, b).0)` needed a scalar position to count as safe.
//   - `var d = drain(w, out)` earned no box credit for `d`, because
//     `drain`'s slow path returns `flush(w, b)` — a call, not a literal.
//   - with the tuple return admitted, a FRESH local handed back bare by a
//     callee inside that tuple (numfmt's `options`) was freed by the exit
//     sweep while the tuple carried it — the top-level `return g(o)` form
//     already had that hazard; the sweep now keeps it like a bare return.
//
// Each case runs at two round counts and the number of unreclaimed blocks
// must not move: a leak of this family is one block per iteration, and a
// fixed residue is the caller's own final block. Exits are the interpreter's.

const tupleHandbackCommon = `struct Block { buf: u8[], n: i32 }

function block_new(): Block {
  return Block { buf: __alloc_u8(8192), n: 0 };
}

function reserve(b: Block, k: i32): Block {
  if (b.n + k <= b.buf.len()) {
    return b;
  }
  var wider: u8[] = __alloc_u8(b.n + k + 4096);
  var i: i32 = 0;
  var out: Block = Block { buf: wider, n: b.n };
  while (i < b.n) {
    out = Block { ...out, buf: out.buf.with(i, b.buf[i]) };
    i = i + 1;
  }
  return out;
}

function push_str(b: Block, s: string): Block {
  var out: Block = reserve(b, s.len());
  var i: i32 = 0;
  while (i < s.len()) {
    out = Block { ...out, buf: out.buf.with(out.n, s[i]), n: out.n + 1 };
    i = i + 1;
  }
  return out;
}

function flush(w: i32, b: Block): (i32, Block) {
  if (b.n == 0) {
    return (w, b);
  }
  return (w + b.n, Block { ...b, n: 0 });
}

function drain(w: i32, b: Block): (i32, Block) {
  if (b.n < 4096) {
    return (w, b);
  }
  return flush(w, b);
}

function main(): i32 {
  var b: Block = block_new();
  var r: (i32, Block) = run(0, b, ROUNDS);
  return r.1.n % 100 + (r.1.buf[3] as i32) % 7 + (r.0 / 28000) + b.n;
}
`

var tupleHandbackCases = []struct {
	name string
	run  string
}{
	// The local goes back to the caller inside a tuple, and a second callee
	// rebinds it on one path.
	{"tuple_return", `function flush_fresh(b: Block): Block {
  return Block { ...b, n: 0 };
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = b;
  var i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    if (out.n >= 4096) {
      w = w + out.n;
      out = flush_fresh(out);
    }
    i = i + 1;
  }
  return (w, out);
}`},
	// The handback pair with the other element read in between: dec_seq.
	{"handback_other_elem_read", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = b;
  var i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    if (out.n >= 4096) {
      var d: (i32, Block) = flush(w, out);
      w = d.0;
      out = d.1;
    }
    i = i + 1;
  }
  return (w, out);
}`},
	// The identity handback every iteration: drain returns its parameter
	// inside the tuple on the common path. print_numbers.
	{"identity_handback_each_round", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = b;
  var i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    var d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
	// The local handed to a call in STATEMENT position inside a match arm —
	// emit_term's overflow report — its result discarded, and the block
	// reaching the callee only as a projection of a nested tuple-returning
	// call.
	{"void_call_arg_in_arm", `struct Fmt { pre: string, post: string }
function render(v: i32): Option[i32] {
  if (v < 0) {
    return None;
  }
  return Some(v % 2);
}
function finish(w: i32): void {
  exit(w % 100);
}
function overflow(w: i32, b: Block): void {
  finish(flush(w, b).0);
}
function emit_term(w: i32, b: Block, f: Fmt, v: i32): Block {
  var out: Block = push_str(b, f.pre);
  match (render(v)) {
    Some(k) => {
      if (k == 0) {
        out = push_str(out, "12345678901230");
      } else {
        out = push_str(out, "1234567890123");
      }
    },
    None => {
      overflow(w, out);
    }
  }
  return push_str(out, f.post);
}
function terminate(b: Block): Block {
  return push_str(b, "\n");
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var f: Fmt = Fmt { pre: "", post: "" };
  var w: i32 = w0;
  var out: Block = emit_term(w, b, f, 0);
  var i: i32 = 1;
  while (true) {
    if (i > rounds) {
      out = push_str(out, "\n");
      out = emit_term(w, out, f, i);
      return (w, terminate(out));
    }
    out = push_str(out, "\n");
    out = emit_term(w, out, f, i);
    var d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
	// numfmt's `options`: a FRESH local the exit sweep releases, handed back
	// bare by `check` inside the returned tuple. The sweep must keep it — the
	// box IS the result — and `junk` recycles its block if it does not, so
	// the caller's read moves rather than reading intact freed bytes (the
	// quarantine cannot see this one; the interpreter's exit can).
	{"swept_local_handed_back", `function check(b: Block, w: i32): Block {
  if (w < 0) {
    exit(3);
  }
  return b;
}
function build(w0: i32, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var o: Block = block_new();
  var i: i32 = 0;
  while (i < rounds) {
    o = push_str(o, "1234567890123\n");
    var d: (i32, Block) = drain(w, o);
    w = d.0;
    o = d.1;
    i = i + 1;
  }
  return (w + o.n, check(o, w));
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var inner: (i32, Block) = build(w0, rounds);
  var o: Block = inner.1;
  var junk: Block = Block { buf: __alloc_u8(8), n: 77 };
  var out: Block = push_str(b, "x");
  return (inner.0 + junk.n - 77, Block { ...out, n: o.n % 100 + out.n });
}`},
	// swept_local_handed_back's hazard for a credited TUPLE local (#8734): it
	// goes back bare through a returned call, and the caller allocates a
	// same-sized block before reading it, so a sweep that freed it would hand
	// that block to the reader.
	{"swept_tuple_handed_back", `function checkt(t: (i32, i32[]), w: i32): (i32, i32[]) {
  if (w < 0) {
    exit(3);
  }
  return t;
}
function build(w: i32, rounds: i32): (i32, i32[]) {
  var xs: i32[] = [];
  var i: i32 = 0;
  while (i < rounds) {
    xs = xs.append(i % 7);
    i = i + 1;
  }
  var t: (i32, i32[]) = (w, xs);
  return checkt(t, w);
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var r: (i32, i32[]) = build(w0, rounds);
  var junk: i32[] = [];
  var i: i32 = 0;
  while (i < r.1.len()) {
    junk = junk.append(5);
    i = i + 1;
  }
  var out: Block = push_str(b, "x");
  return (r.0 + junk.len() - r.1.len(), Block { ...out, n: out.n + r.1[3] + r.1.len() % 100 });
}`},
	// A call-derived local rather than an alias, with the loop's exit a
	// tuple return whose element is a call taking the local.
	{"call_local_loop_tuple_return", `function terminate(b: Block): Block {
  return push_str(b, "\n");
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = push_str(b, "x");
  var i: i32 = 1;
  while (true) {
    if (i > rounds) {
      out = push_str(out, "end");
      return (w, terminate(out));
    }
    out = push_str(out, "1234567890123\n");
    var d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
}

// tupleHandbackCheckedCases earn their credits only from the checker's type
// annotations, so they run through the self-host CLI, which annotates, and
// not through asm_ir_run, which does not.
var tupleHandbackCheckedCases = []struct {
	name string
	run  string
}{
	// The handback unpacked by a destructure (#8734): re-declaring the loop
	// locals in the same statement, and through fresh binders rebound after.
	// The re-declaration SHADOWS `w` and `out` for the rest of the body, so the
	// outer block is never drained and grows every round; what the case pins
	// is that no generation of either is stranded.
	{"destructure_same_statement", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = b;
  var i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    var (w, out) = drain(w, out);
    i = i + 1;
  }
  return (w, out);
}`},
	{"destructure_fresh_binders", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var w: i32 = w0;
  var out: Block = b;
  var i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    var (w2, o2) = drain(w, out);
    w = w2;
    out = o2;
    i = i + 1;
  }
  return (w, out);
}`},
	// The same hazard for a credited STRING accumulator.
	{"swept_string_handed_back", `function checks(acc: string, w: i32): string {
  if (w < 0) {
    exit(3);
  }
  return acc;
}
function build(w: i32, rounds: i32): string {
  var acc: string = "";
  var i: i32 = 0;
  while (i < rounds) {
    acc = acc + "ab";
    i = i + 1;
  }
  return checks(acc, w);
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  var s: string = build(w0, rounds);
  var junk: string = "";
  var i: i32 = 0;
  while (i < s.len()) {
    junk = junk + "z";
    i = i + 1;
  }
  var out: Block = push_str(b, "x");
  return (w0 + junk.len() - s.len(), Block { ...out, n: out.n + (s[3] as i32) % 7 + s.len() % 100 });
}`},
}

func tupleHandbackSrc(run string, rounds int) string {
	return strings.ReplaceAll(run+"\n"+tupleHandbackCommon, "ROUNDS", strconv.Itoa(rounds))
}

// tupleHandbackCensus reads the census a run printed and returns its
// unreclaimed block count.
func tupleHandbackCensus(t *testing.T, name, stderr string) int64 {
	t.Helper()
	allocs, frees, _ := parseLeakcheck(t, name, stderr)
	if allocs == 0 {
		t.Fatalf("%s: allocs=0 — the probe exercised no allocation", name)
	}
	return allocs - frees
}

// checkTupleHandback asserts the two censuses agree and that the residue is a
// handful of blocks, not a per-round one.
func checkTupleHandback(t *testing.T, name string, small, large int64, rounds [2]int) {
	t.Helper()
	if small != large {
		t.Errorf("%s: %d blocks unreclaimed at %d rounds, %d at %d — the leak scales with the loop",
			name, small, rounds[0], large, rounds[1])
	}
	if small > 8 {
		t.Errorf("%s: %d blocks unreclaimed at %d rounds — more than the caller's own residue", name, small, rounds[0])
	}
}

var tupleHandbackRounds = [2]int{200, 2000}

func TestSelfHostTupleHandbackReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			var leaked [2]int64
			for k, rounds := range tupleHandbackRounds {
				src := tupleHandbackSrc(tc.run, rounds)
				want := interpExit(t, interpBin, src)
				asm := runCaptureEnv(t, runner, driverBin, []byte(src),
					[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}, "-ir")
				if len(asm) == 0 {
					t.Fatal("self-host compiler emitted 0 bytes")
				}
				bin := buildBin(t, gcc, dir, "thb_"+tc.name+"_"+strconv.Itoa(rounds), string(asm))
				stderr, code := runCaptureStderrExit(t, runner, bin)
				if code != want {
					t.Fatalf("%s at %d rounds exited %d, want %d (interp oracle)", tc.name, rounds, code, want)
				}
				leaked[k] = tupleHandbackCensus(t, tc.name, stderr)
			}
			checkTupleHandback(t, tc.name, leaked[0], leaked[1], tupleHandbackRounds)
		})
	}
}

// The sanitizer leg: the quarantine and the over-release trap must stay
// silent — the rebind release now runs where it used to be refused, so the
// direction to guard is a free too many, not a leak.
func TestSelfHostTupleHandbackReclaimSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tupleHandbackSrc(tc.run, tupleHandbackRounds[1])
			want := interpExit(t, interpBin, src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"}, "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "thbsan_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("%s exited %d under the sanitizer, want %d (interp oracle)", tc.name, code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s: %s", tc.name, line)
				}
			}
		})
	}
}

// The CLI leg compiles every case, the annotated ones included, the way
// production does: through the self-host CLI with the checker's annotations.
// Each case gets the census at both round counts and one sanitizer run.
func TestSelfHostTupleHandbackReclaimCLIX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	build := func(t *testing.T, src, mode string) string {
		t.Helper()
		return cli.x86Binary(t, mustWrite(t, t.TempDir(), "main.fern", src), "FERN_STRICT_IR=1", mode)
	}
	cases := append(append(tupleHandbackCases[:0:0], tupleHandbackCases...), tupleHandbackCheckedCases...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var leaked [2]int64
			var want int
			for k, rounds := range tupleHandbackRounds {
				src := tupleHandbackSrc(tc.run, rounds)
				want = interpExit(t, interpBin, src)
				stderr, code := runCaptureStderrExit(t, cli.runner, build(t, src, "FERN_LEAKCHECK=1"))
				if code != want {
					t.Fatalf("%s at %d rounds exited %d, want %d (interp oracle)", tc.name, rounds, code, want)
				}
				leaked[k] = tupleHandbackCensus(t, tc.name, stderr)
			}
			checkTupleHandback(t, tc.name, leaked[0], leaked[1], tupleHandbackRounds)

			// want is still the last round count's answer.
			src := tupleHandbackSrc(tc.run, tupleHandbackRounds[1])
			stderr, code := runCaptureStderrExit(t, cli.runner, build(t, src, "FERN_SANITIZE=1"))
			if code != want {
				t.Fatalf("%s exited %d under the sanitizer, want %d (interp oracle)", tc.name, code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Errorf("%s: %s", tc.name, line)
				}
			}
		})
	}
}

func TestSelfHostTupleHandbackReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			var leaked [2]int64
			for k, rounds := range tupleHandbackRounds {
				src := tupleHandbackSrc(tc.run, rounds)
				want := interpExit(t, interpBin, src)
				asm := runCaptureEnv(t, x86runner, driverBin, []byte(src),
					[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux")
				if len(asm) == 0 {
					t.Fatal("self-host arm64 compiler emitted 0 bytes")
				}
				bin := buildBinArm64(t, arm64gcc, dir, "thb_"+tc.name+"_"+strconv.Itoa(rounds), string(asm))
				cmd := runArm64Bin(qemu, bin)
				var errBuf strings.Builder
				cmd.Stderr = &errBuf
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != want {
					t.Fatalf("%s at %d rounds exited %d, want %d (interp oracle)", tc.name, rounds, code, want)
				}
				leaked[k] = tupleHandbackCensus(t, tc.name, errBuf.String())
			}
			checkTupleHandback(t, tc.name, leaked[0], leaked[1], tupleHandbackRounds)
		})
	}
}

func TestSelfHostTupleHandbackReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm tuple handback e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			var leaked [2]int64
			for k, rounds := range tupleHandbackRounds {
				src := tupleHandbackSrc(tc.run, rounds)
				want := interpExit(t, interpBin, src)
				wat := wasmLcCompile(t, runner, driverBin, src, []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
				stderr, code := wasmLcRun(t, dir, "thb_"+tc.name+"_"+strconv.Itoa(rounds), wat)
				if code != want {
					t.Fatalf("%s at %d rounds exited %d, want %d (interp oracle)", tc.name, rounds, code, want)
				}
				leaked[k] = tupleHandbackCensus(t, tc.name, stderr)
			}
			checkTupleHandback(t, tc.name, leaked[0], leaked[1], tupleHandbackRounds)
		})
	}
}

// runCaptureStderrExit runs a built program and returns its stderr and exit
// code; a signal death is fatal, since a census cannot be read off one.
func runCaptureStderrExit(t *testing.T, runner []string, bin string) (string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally", bin)
	}
	return errBuf.String(), cmd.ProcessState.ExitCode()
}
