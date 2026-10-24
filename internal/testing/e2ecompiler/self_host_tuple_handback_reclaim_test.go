package e2ecompiler

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const tupleHandbackCommon = `struct Block { buf: u8[], n: i32 }

function block_new(): Block {
  return Block { buf: __alloc_u8(8192), n: 0 };
}

function reserve(b: Block, k: i32): Block {
  if (b.n + k <= b.buf.len()) {
    return b;
  }
  let wider: u8[] = __alloc_u8(b.n + k + 4096);
  let i: i32 = 0;
  let out: Block = Block { buf: wider, n: b.n };
  while (i < b.n) {
    out = Block { ...out, buf: out.buf.with(i, b.buf[i]) };
    i = i + 1;
  }
  return out;
}

function push_str(b: Block, s: string): Block {
  let out: Block = reserve(b, s.len());
  let i: i32 = 0;
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
  let b: Block = block_new();
  let r: (i32, Block) = run(0, b, ROUNDS);
  return r.1.n % 100 + (r.1.buf[3] as i32) % 7 + (r.0 / 28000) + b.n;
}
`

var tupleHandbackCases = []struct {
	name string
	run  string
}{
	{"tuple_return", `function flush_fresh(b: Block): Block {
  return Block { ...b, n: 0 };
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = b;
  let i: i32 = 0;
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
	{"handback_other_elem_read", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = b;
  let i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    if (out.n >= 4096) {
      let d: (i32, Block) = flush(w, out);
      w = d.0;
      out = d.1;
    }
    i = i + 1;
  }
  return (w, out);
}`},
	{"identity_handback_each_round", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = b;
  let i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    let d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
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
  let out: Block = push_str(b, f.pre);
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
  let f: Fmt = Fmt { pre: "", post: "" };
  let w: i32 = w0;
  let out: Block = emit_term(w, b, f, 0);
  let i: i32 = 1;
  while (true) {
    if (i > rounds) {
      out = push_str(out, "\n");
      out = emit_term(w, out, f, i);
      return (w, terminate(out));
    }
    out = push_str(out, "\n");
    out = emit_term(w, out, f, i);
    let d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
	{"swept_local_handed_back", `function check(b: Block, w: i32): Block {
  if (w < 0) {
    exit(3);
  }
  return b;
}
function build(w0: i32, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let o: Block = block_new();
  let i: i32 = 0;
  while (i < rounds) {
    o = push_str(o, "1234567890123\n");
    let d: (i32, Block) = drain(w, o);
    w = d.0;
    o = d.1;
    i = i + 1;
  }
  return (w + o.n, check(o, w));
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let inner: (i32, Block) = build(w0, rounds);
  let o: Block = inner.1;
  let junk: Block = Block { buf: __alloc_u8(8), n: 77 };
  let out: Block = push_str(b, "x");
  return (inner.0 + junk.n - 77, Block { ...out, n: o.n % 100 + out.n });
}`},
	{"swept_tuple_handed_back", `function checkt(t: (i32, i32[]), w: i32): (i32, i32[]) {
  if (w < 0) {
    exit(3);
  }
  return t;
}
function build(w: i32, rounds: i32): (i32, i32[]) {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < rounds) {
    xs = xs.append(i % 7);
    i = i + 1;
  }
  let t: (i32, i32[]) = (w, xs);
  return checkt(t, w);
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let r: (i32, i32[]) = build(w0, rounds);
  let junk: i32[] = [];
  let i: i32 = 0;
  while (i < r.1.len()) {
    junk = junk.append(5);
    i = i + 1;
  }
  let out: Block = push_str(b, "x");
  return (r.0 + junk.len() - r.1.len(), Block { ...out, n: out.n + r.1[3] + r.1.len() % 100 });
}`},
	{"call_local_loop_tuple_return", `function terminate(b: Block): Block {
  return push_str(b, "\n");
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = push_str(b, "x");
  let i: i32 = 1;
  while (true) {
    if (i > rounds) {
      out = push_str(out, "end");
      return (w, terminate(out));
    }
    out = push_str(out, "1234567890123\n");
    let d: (i32, Block) = drain(w, out);
    w = d.0;
    out = d.1;
    i = i + 1;
  }
  return (w, out);
}`},
}

var tupleHandbackCheckedCases = []struct {
	name string
	run  string
}{
	{"destructure_same_statement", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = b;
  let i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    let (w, out) = drain(w, out);
    i = i + 1;
  }
  return (w, out);
}`},
	{"destructure_fresh_binders", `function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let w: i32 = w0;
  let out: Block = b;
  let i: i32 = 0;
  while (i < rounds) {
    out = push_str(out, "1234567890123\n");
    let (w2, o2) = drain(w, out);
    w = w2;
    out = o2;
    i = i + 1;
  }
  return (w, out);
}`},
	{"swept_string_handed_back", `function checks(acc: string, w: i32): string {
  if (w < 0) {
    exit(3);
  }
  return acc;
}
function build(w: i32, rounds: i32): string {
  let acc: string = "";
  let i: i32 = 0;
  while (i < rounds) {
    acc = acc + "ab";
    i = i + 1;
  }
  return checks(acc, w);
}
function run(w0: i32, b: Block, rounds: i32): (i32, Block) {
  let s: string = build(w0, rounds);
  let junk: string = "";
  let i: i32 = 0;
  while (i < s.len()) {
    junk = junk + "z";
    i = i + 1;
  }
  let out: Block = push_str(b, "x");
  return (w0 + junk.len() - s.len(), Block { ...out, n: out.n + (s[3] as i32) % 7 + s.len() % 100 });
}`},
}

func tupleHandbackSrc(run string, rounds int) string {
	return strings.ReplaceAll(run+"\n"+tupleHandbackCommon, "ROUNDS", strconv.Itoa(rounds))
}

func tupleHandbackCensus(t *testing.T, name, stderr string) int64 {
	t.Helper()
	allocs, frees, _ := parseLeakcheck(t, name, stderr)
	if allocs == 0 {
		t.Fatalf("%s: allocs=0 — the probe exercised no allocation", name)
	}
	return allocs - frees
}

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			var leaked [2]int64
			for k, rounds := range tupleHandbackRounds {
				src := tupleHandbackSrc(tc.run, rounds)
				want := interpExit(t, interpBin, src)
				asm := runCaptureEnv(t, runner, driverBin, []byte(src),
					[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"})
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

func TestSelfHostTupleHandbackReclaimSanitizeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleHandbackCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tupleHandbackSrc(tc.run, tupleHandbackRounds[1])
			want := interpExit(t, interpBin, src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"})
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

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
