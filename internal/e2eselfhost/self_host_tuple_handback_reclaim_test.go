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
// the things that refused or defeated the local's rebind release:
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
