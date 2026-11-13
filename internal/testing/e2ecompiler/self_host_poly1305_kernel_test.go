package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// poly1305KernelProg prints RFC 8439's Poly1305 tag through
// __poly1305_blocks. Then it prints the state after the kernel absorbs each
// length to 1024 and some longer lengths, from three start states: a pattern,
// all ones (the largest r after the clamp, with 0xff messages for the most
// carries), and h above p with high bits in h2 that the kernel does not read.
// The exit code is not 0 when a state with another holder, or a literal, is
// written in place, or when two calls split at a block boundary disagree with
// one call.
const poly1305KernelProg = `function pattern(n: i32, mul: i32, add: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i * mul + add) & 255) as u8);
    i = i + 1;
  }
  return out;
}

function filled(n: i32, v: u8): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, v);
    i = i + 1;
  }
  return out;
}

function hex(b: u8[]): string {
  let d: string[] = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"];
  let out: string = "";
  for x in b {
    out = out + d[x as i32 / 16] + d[x as i32 % 16];
  }
  return out;
}

function same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

// A state: h from hb (24 bytes) and r from rb (16 bytes).
function state(hb: [u8], rb: [u8]): u8[] {
  let st: u8[] = __alloc_u8(40);
  let i: i32 = 0;
  while (i < 24) {
    st = st.with(i, hb[i]);
    i = i + 1;
  }
  while (i < 40) {
    st = st.with(i, rb[i - 24]);
    i = i + 1;
  }
  return st;
}

function le64(b: [u8], at: i32): u64 {
  let v: u64 = 0;
  let i: i32 = 7;
  while (i >= 0) {
    v = v << 8 | b[at + i] as u64;
    i = i - 1;
  }
  return v;
}

// The tag of RFC 8439: h, which the kernel leaves below p, plus s, mod 2^128.
function tag(st: [u8], s: [u8]): u8[] {
  let h0 = le64(st, 0);
  let h1 = le64(st, 8);
  let s0 = le64(s, 0);
  let t0 = h0 + s0;
  let c: u64 = if (t0 < h0) { 1 } else { 0 };
  let t1 = h1 + le64(s, 8) + c;
  let out: u8[] = __alloc_u8(16);
  let i: i32 = 0;
  while (i < 8) {
    out = out.with(i, (t0 >> (8 * i) as u64 & 255) as u8).with(8 + i, (t1 >> (8 * i) as u64 & 255) as u8);
    i = i + 1;
  }
  return out;
}

// The state has another holder (starts in main), so the kernel copies it before it writes.
function sweep(name: string, st: u8[], n: i32, msg: u8[]): void {
  let out: u8[] = __poly1305_blocks(st, msg, 5, n);
  print(name + " " + n.to_string() + " " + hex(out));
}

// A state with another holder is copied before the kernel writes it, a literal is never written, and two calls
// split at a block boundary give the state of one call.
function shared(st: u8[]): i32 {
  let kept: u8[] = st;
  let snapshot: u8[] = state(st[0:24], st[24:40]);
  let out: u8[] = __poly1305_blocks(st, pattern(700, 3, 1), 0, 700);
  if (!same(kept, snapshot) || same(out, kept)) {
    return 1;
  }
  let lit: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
  let once: u8[] = __poly1305_blocks(lit, pattern(20, 1, 1), 0, 20);
  let again: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
  if (!same(lit, again) || same(once, lit)) {
    return 2;
  }
  let msg: u8[] = pattern(3000, 7, 3);
  let whole: u8[] = __poly1305_blocks(state(st[0:24], st[24:40]), msg, 0, 2999);
  for cut in [0, 16, 48, 256, 1024, 1040, 2992] {
    let first: u8[] = __poly1305_blocks(state(st[0:24], st[24:40]), msg, 0, cut);
    let both: u8[] = __poly1305_blocks(first, msg, cut, 2999 - cut);
    if (!same(both, whole)) {
      return 3;
    }
  }
  return 0;
}

// h = 2^130 - 3, which is above p, with high bits in h2 that the kernel does not read.
function above_p(): u8[] {
  let h: u8[] = filled(24, 255 as u8).with(0, 253 as u8).with(16, 7 as u8);
  return h;
}

function main(): i32 {
  // RFC 8439 2.5.2.
  let key: [u8] = [0x85u8, 0xd6, 0xbe, 0x78, 0x57, 0x55, 0x6d, 0x33, 0x7f, 0x44, 0x52, 0xfe, 0x42, 0xd5, 0x06, 0xa8, 0x01, 0x03, 0x80, 0x8a, 0xfb, 0x0d, 0xb2, 0xfd, 0x4a, 0xbf, 0xf6, 0xaf, 0x41, 0x49, 0xf5, 0x1b];
  let forum: [u8] = "Cryptographic Forum Research Group".as_bytes();
  let st: u8[] = __poly1305_blocks(state(__alloc_u8(24), key[0:16]), forum, 0, forum.len());
  print("rfc " + hex(tag(st, key[16:32])));
  let sweeps: i32[] = [1279, 1280, 1281, 2047, 2048, 2049, 4095, 4096, 4097, 9000];
  let msg: u8[] = pattern(9009, 13, 7);
  let ones: u8[] = filled(9009, 255 as u8);
  let starts: u8[][] = [
    state(pattern(24, 5, 9), pattern(16, 11, 3)),
    state(filled(24, 255 as u8), filled(16, 255 as u8)),
    state(above_p(), filled(16, 255 as u8)),
  ];
  let names: string[] = ["pattern", "ones", "nearp"];
  for k in 0..starts.len() {
    let m: u8[] = if (k == 1) { ones } else { msg };
    let n: i32 = 0;
    while (n <= 1024) {
      sweep(names[k], starts[k], n, m);
      n = n + 1;
    }
    for big in sweeps {
      sweep(names[k], starts[k], big, m);
    }
  }
  let viewed: u8[] = pattern(3000, 3, 5);
  print("view " + hex(__poly1305_blocks(state(pattern(24, 1, 1), pattern(16, 9, 2)), viewed[7:2990], 5, 2000)));
  return shared(starts[0]);
}
`

// poly1305KernelSweep is the lengths the program prints after the lengths to 1024.
var poly1305KernelSweep = []int{1279, 1280, 1281, 2047, 2048, 2049, 4095, 4096, 4097, 9000}

// poly1305P is 2^130 - 5.
var poly1305P = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))

// poly1305Model is the contract of __poly1305_blocks: the state is h0, h1, h2,
// r0 and r1 as little-endian u64. Only the low 2 bits of h2 are read, and r is
// clamped. Each 16-byte block is added with 2^128 and multiplied by r, mod p. A
// short last block gets a 1 byte after its end and no 2^128. h leaves fully
// reduced, and r is not written.
func poly1305Model(st, msg []byte) []byte {
	out := append([]byte(nil), st...)
	word := func(at int) *big.Int { return new(big.Int).SetUint64(binary.LittleEndian.Uint64(out[at:])) }
	h := new(big.Int).Lsh(new(big.Int).SetUint64(binary.LittleEndian.Uint64(out[16:])&3), 128)
	h.Add(h, new(big.Int).Lsh(word(8), 64)).Add(h, word(0))
	r := new(big.Int).Lsh(new(big.Int).SetUint64(binary.LittleEndian.Uint64(out[32:])&0x0ffffffc0ffffffc), 64)
	r.Add(r, new(big.Int).SetUint64(binary.LittleEndian.Uint64(out[24:])&0x0ffffffc0fffffff))
	for off := 0; off < len(msg); off += 16 {
		block := make([]byte, 17)
		k := copy(block[:16], msg[off:])
		block[k] = 1
		for i, j := 0, 16; i < j; i, j = i+1, j-1 {
			block[i], block[j] = block[j], block[i]
		}
		h.Add(h, new(big.Int).SetBytes(block)).Mul(h, r).Mod(h, poly1305P)
	}
	h.Mod(h, poly1305P)
	be := h.FillBytes(make([]byte, 24))
	for i := 0; i < 24; i++ {
		out[i] = be[23-i]
	}
	return out
}

func poly1305Filled(n int, v byte) []byte {
	return bytes.Repeat([]byte{v}, n)
}

func poly1305KernelWant(t *testing.T) string {
	t.Helper()
	key, err := hex.DecodeString("85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b")
	if err != nil {
		t.Fatal(err)
	}
	st := poly1305Model(append(make([]byte, 24), key[:16]...), []byte("Cryptographic Forum Research Group"))
	lo := binary.LittleEndian.Uint64(st) + binary.LittleEndian.Uint64(key[16:])
	carry := uint64(0)
	if lo < binary.LittleEndian.Uint64(st) {
		carry = 1
	}
	hi := binary.LittleEndian.Uint64(st[8:]) + binary.LittleEndian.Uint64(key[24:]) + carry
	tag := binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, lo), hi)
	if got := hex.EncodeToString(tag); got != "a8061dc1305136c6c22b8baf0c0127a9" {
		t.Fatalf("the Go model gives %s for RFC 8439 2.5.2", got)
	}
	var b strings.Builder
	b.WriteString("rfc a8061dc1305136c6c22b8baf0c0127a9\n")
	aboveP := poly1305Filled(24, 255)
	aboveP[0], aboveP[16] = 253, 7
	starts := [][]byte{
		append(chacha20Pattern(24, 5, 9), chacha20Pattern(16, 11, 3)...),
		poly1305Filled(40, 255),
		append(aboveP, poly1305Filled(16, 255)...),
	}
	names := []string{"pattern", "ones", "nearp"}
	msg, ones := chacha20Pattern(9009, 13, 7), poly1305Filled(9009, 255)
	var lengths []int
	for n := 0; n <= 1024; n++ {
		lengths = append(lengths, n)
	}
	for k := range starts {
		m := msg
		if k == 1 {
			m = ones
		}
		for _, n := range append(lengths, poly1305KernelSweep...) {
			fmt.Fprintf(&b, "%s %d %s\n", names[k], n, hex.EncodeToString(poly1305Model(starts[k], m[5:5+n])))
		}
	}
	viewed := chacha20Pattern(3000, 3, 5)[7:2990]
	fmt.Fprintf(&b, "view %s\n", hex.EncodeToString(poly1305Model(append(chacha20Pattern(24, 1, 1), chacha20Pattern(16, 9, 2)...), viewed[5:2005])))
	return b.String()
}

func poly1305KernelCheck(t *testing.T, got []byte, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("exited %d, want 0: see shared, which checks the copies of shared states and the split calls", exit)
	}
	gl, wl := strings.Split(string(got), "\n"), strings.Split(poly1305KernelWant(t), "\n")
	for i := range wl {
		if i >= len(gl) || gl[i] != wl[i] {
			g := "(missing)"
			if i < len(gl) {
				g = gl[i]
			}
			t.Fatalf("line %d: got %q, want %q", i+1, g, wl[i])
		}
	}
	if len(gl) != len(wl) {
		t.Fatalf("output has %d lines, want %d", len(gl), len(wl))
	}
}

func poly1305Run(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally\n%s", errb.String())
	}
	poly1305KernelCheck(t, out.Bytes(), cmd.ProcessState.ExitCode())
}

func poly1305Source(t *testing.T, prog string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "poly1305.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostPoly1305KernelX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := poly1305Source(t, poly1305KernelProg)
	t.Run("in-process", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		poly1305Run(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("gas", func(t *testing.T) {
		poly1305Run(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
}

func TestSelfHostPoly1305KernelArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := poly1305Source(t, poly1305KernelProg)
	t.Run("in-process", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		poly1305Run(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)))
	})
	t.Run("gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		poly1305Run(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))))
	})
}

func TestSelfHostPoly1305KernelWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	poly1305Run(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, poly1305Source(t, poly1305KernelProg), "FERN_STRICT_IR=1")))
}

func TestSelfHostInterpPoly1305Kernel(t *testing.T) {
	if strings.HasPrefix(poly1305KernelProg, "import") {
		t.Fatal("the self-host interpreter driver runs single-file programs only")
	}
	t.Run("go", func(t *testing.T) {
		got, exit := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), poly1305Source(t, poly1305KernelProg))
		poly1305KernelCheck(t, got, exit)
	})
	t.Run("self-host", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		cmd := runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run"))
		cmd.Stdin = strings.NewReader(poly1305KernelProg)
		poly1305Run(t, cmd)
	})
}

// TestSelfHostPoly1305KernelCensus runs the kernel program under the leak
// census on each target. Every allocation is freed, and the copies that the
// kernel makes of shared states are among them.
func TestSelfHostPoly1305KernelCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := poly1305Source(t, poly1305KernelProg)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if target == "wasm32-wasi" {
				chacha20CensusBalances(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src, "FERN_LEAKCHECK=1")))
				return
			}
			bin := filepath.Join(t.TempDir(), "prog")
			cmd := cli.compileCmd(target, bin, src)
			cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if err := os.Chmod(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if target == "arm64-linux" {
				chacha20CensusBalances(t, e2eharness.RunArm64Bin(e2eharness.Arm64Runner(t), bin))
				return
			}
			chacha20CensusBalances(t, runX86_64Bin(cli.runner, bin))
		})
	}
}

// poly1305KernelMisuses each stop the program before the kernel reaches
// outside the message or the state: a range past the end of the message, a
// negative offset or length, and a state that is not 40 bytes.
var poly1305KernelMisuses = []string{
	"__poly1305_blocks(__alloc_u8(40), msg, 4, 17)",
	"__poly1305_blocks(__alloc_u8(40), msg, 0 - 1, 4)",
	"__poly1305_blocks(__alloc_u8(40), msg, 0, 0 - 16)",
	"__poly1305_blocks(__alloc_u8(39), msg, 0, 16)",
	"__poly1305_blocks(__alloc_u8(41), msg, 0, 16)",
	"__poly1305_blocks(__alloc_u8(40), msg[2:18], 0, 17)",
}

func poly1305KernelMisuseProg(call string) string {
	return `function main(): i32 {
  let msg: u8[] = __alloc_u8(20);
  let out: u8[] = ` + call + `;
  print("survived");
  return out.len() - out.len();
}
`
}

func poly1305MisuseStops(t *testing.T, run func(src string) *exec.Cmd) {
	t.Helper()
	for _, call := range poly1305KernelMisuses {
		out, _ := run(poly1305Source(t, poly1305KernelMisuseProg(call))).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostPoly1305KernelMisuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		poly1305MisuseStops(t, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src)) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		poly1305MisuseStops(t, func(src string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)) })
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		wasmtime := e2eharness.Wasmtime(t)
		poly1305MisuseStops(t, func(src string) *exec.Cmd {
			return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
		})
	})
	t.Run("interpreters", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		oracle := buildLangBinForInterp(t)
		for _, call := range poly1305KernelMisuses {
			prog := poly1305KernelMisuseProg(call)
			if got := interpExitStdin(t, oracle, prog, ""); got == 0 {
				t.Errorf("Go interpreter: %s returned instead of stopping the program", call)
			}
			if got := runDriverExit(t, runner, driver, []byte(prog)); got == 0 {
				t.Errorf("self-host interpreter: %s returned instead of stopping the program", call)
			}
		}
	})
}
