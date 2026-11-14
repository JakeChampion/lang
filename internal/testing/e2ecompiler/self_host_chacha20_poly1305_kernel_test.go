package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// chachaPolyKernelProg prints RFC 8439's AEAD answer through
// __chacha20_poly1305_seal_into. Then it seals each length to 1024 and lengths
// around the groups of four and eight blocks, and prints an FNV-1a digest of the
// whole buffer: 3 bytes, the additional data, the text, the tag and 2 bytes.
// The additional data is n % 23 bytes. A block counter that wraps, and views
// for the key and the counter, come last.
//
// Each seal must give the buffer of the two kernels __chacha20_xor_into and
// __poly1305_blocks, and each open must give the text back with the verdict 0.
// An open of a buffer with one changed bit in the additional data, the
// ciphertext or the tag must give the verdict 1. The exit code counts the
// failures, up to 99, and a buffer with another holder, or a literal, that is
// written in place adds 100.
const chachaPolyKernelProg = `function pattern(n: i32, mul: i32, add: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i * mul + add) & 255) as u8);
    i = i + 1;
  }
  return out;
}

function hexval(c: u8): i32 {
  if (c >= 97 as u8) {
    return c as i32 - 87;
  }
  return c as i32 - 48;
}

function unhex(s: string): u8[] {
  let b: [u8] = s.as_bytes();
  let out: u8[] = __alloc_u8(b.len() / 2);
  let i: i32 = 0;
  while (i < out.len()) {
    out = out.with(i, (hexval(b[2 * i]) * 16 + hexval(b[2 * i + 1])) as u8);
    i = i + 1;
  }
  return out;
}

function hex(b: [u8]): string {
  let d: string[] = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"];
  let out: string = "";
  for x in b {
    out = out + d[x as i32 / 16] + d[x as i32 % 16];
  }
  return out;
}

// FNV-1a over every byte of the buffer, so a write outside the range changes it too.
function fnv(b: u8[]): i64 {
  let h: i64 = 2166136261;
  for x in b {
    h = (h ^ x as i64) * 16777619 & 4294967295;
  }
  return h;
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

function le64(b: [u8], at: i32): u64 {
  let v: u64 = 0;
  let i: i32 = 7;
  while (i >= 0) {
    v = v << 8 | b[at + i] as u64;
    i = i - 1;
  }
  return v;
}

function put64(own b: u8[], at: i32, v: u64): u8[] {
  let i: i32 = 0;
  while (i < 8) {
    b = b.with(at + i, (v >> (8 * i) as u64 & 255) as u8);
    i = i + 1;
  }
  return b;
}

// b[from:from + len] into the state, with zeros after a short last block.
function padded(own st: u8[], b: [u8], from: i32, len: i32): u8[] {
  let full: i32 = len - len % 16;
  st = __poly1305_blocks(st, b, from, full);
  if (full == len) {
    return st;
  }
  let last: u8[] = __alloc_u8(16);
  let i: i32 = 0;
  while (i < len - full) {
    last = last.with(i, b[from + full + i]);
    i = i + 1;
  }
  return __poly1305_blocks(st, last, 0, 16);
}

// The tag of the two kernels: the one-time key from block ctr, and Poly1305 over the padded additional data,
// the padded ciphertext and the two lengths.
function tag_of(key: [u8], ctr: [u8], buf: [u8], aad_at: i32, aad_n: i32, at: i32, n: i32): u8[] {
  let otk: u8[] = __chacha20_xor_into(key, ctr, __alloc_u8(32), 0, 32);
  let st: u8[] = __alloc_u8(40);
  let i: i32 = 0;
  while (i < 16) {
    st = st.with(24 + i, otk[i]);
    i = i + 1;
  }
  st = padded(padded(st, buf, aad_at, aad_n), buf, at, n);
  st = __poly1305_blocks(st, put64(put64(__alloc_u8(16), 0, aad_n as u64), 8, n as u64), 0, 16);
  let h0: u64 = le64(st, 0);
  let t0: u64 = h0 + le64(otk, 16);
  let c: u64 = if (t0 < h0) { 1 } else { 0 };
  return put64(put64(__alloc_u8(16), 0, t0), 8, le64(st, 8) + le64(otk, 24) + c);
}

// The counter block of the text: the block counter plus one, which wraps.
function next_ctr(ctr: [u8]): u8[] {
  let out: u8[] = __alloc_u8(16);
  let carry: i32 = 1;
  let i: i32 = 0;
  while (i < 16) {
    let v: i32 = ctr[i] as i32;
    if (i < 4) {
      v = v + carry;
      carry = v / 256;
    }
    out = out.with(i, (v & 255) as u8);
    i = i + 1;
  }
  return out;
}

function two_seal(key: [u8], ctr: [u8], own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): u8[] {
  buf = __chacha20_xor_into(key, next_ctr(ctr), buf, at, n);
  let tag: u8[] = tag_of(key, ctr, buf, aad_at, aad_n, at, n);
  let i: i32 = 0;
  while (i < 16) {
    buf = buf.with(at + n + i, tag[i]);
    i = i + 1;
  }
  return buf;
}

// Seals n bytes, opens them, and opens them with one bit changed. The result counts the failures.
function sweep(name: string, key: [u8], ctr: [u8], n: i32): i32 {
  let aad_n: i32 = n % 23;
  let at: i32 = 3 + aad_n;
  let size: i32 = at + n + 18;
  let sealed: u8[] = __chacha20_poly1305_seal_into(key, ctr, pattern(size, 3, 1), 3, aad_n, at, n);
  print(name + " " + n.to_string() + " " + fnv(sealed).to_string());
  let bad: i32 = 0;
  if (!same(sealed, two_seal(key, ctr, pattern(size, 3, 1), 3, aad_n, at, n))) {
    bad = bad + 1;
  }
  let opened: u8[] = __chacha20_poly1305_open_into(key, ctr, sealed, 3, aad_n, at, n);
  let text: u8[] = pattern(size, 3, 1);
  if (!same(opened[0:at + n], text[0:at + n]) || opened[at + n] != 0 || !same(opened[at + n + 1:size], sealed[at + n + 1:size])) {
    bad = bad + 1;
  }
  let flip: i32 = 3 + (n * 7 + 5) % (aad_n + n + 16);
  let forged: u8[] = sealed.with(flip, sealed[flip] ^ (1 << (n % 8)) as u8);
  if (__chacha20_poly1305_open_into(key, ctr, forged, 3, aad_n, at, n)[at + n] != 1) {
    bad = bad + 1;
  }
  return bad;
}

// A buffer with another holder is copied before the kernel writes it, and a literal's storage is never written.
function shared(key: [u8], ctr: [u8]): i32 {
  let buf: u8[] = pattern(60, 3, 1);
  let kept: u8[] = buf;
  let out: u8[] = __chacha20_poly1305_seal_into(key, ctr, buf, 0, 4, 4, 20);
  if (!same(kept, pattern(60, 3, 1)) || same(out, kept) || !same(out[0:4], kept[0:4]) || !same(out[40:60], kept[40:60])) {
    return 100;
  }
  let lit: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20];
  let once: u8[] = __chacha20_poly1305_seal_into(key, ctr, lit, 0, 0, 0, 4);
  let again: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20];
  if (!same(lit, again) || same(once, lit)) {
    return 100;
  }
  let back: u8[] = __chacha20_poly1305_open_into(key, ctr, once, 0, 0, 0, 4);
  if (!same(once, __chacha20_poly1305_seal_into(key, ctr, again, 0, 0, 0, 4)) || !same(back[0:4], lit[0:4]) || back[4] != 0) {
    return 100;
  }
  return 0;
}

function main(): i32 {
  // RFC 8439 2.8.2.
  let rkey: u8[] = pattern(32, 1, 128);
  let rctr: u8[] = unhex("00000000070000004041424344454647");
  let sunscreen: [u8] = "Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.".as_bytes();
  let rbuf: u8[] = __alloc_u8(12 + sunscreen.len() + 16);
  let aad: u8[] = unhex("50515253c0c1c2c3c4c5c6c7");
  let i: i32 = 0;
  while (i < 12 + sunscreen.len()) {
    rbuf = rbuf.with(i, if (i < 12) { aad[i] } else { sunscreen[i - 12] });
    i = i + 1;
  }
  print("rfc " + hex(__chacha20_poly1305_seal_into(rkey, rctr, rbuf, 0, 12, 12, sunscreen.len())[12:12 + sunscreen.len() + 16]));
  let key: u8[] = pattern(32, 13, 5);
  let ctr: u8[] = pattern(16, 7, 3).with(0, 0 as u8).with(1, 0 as u8).with(2, 0 as u8).with(3, 0 as u8);
  let bad: i32 = 0;
  let n: i32 = 0;
  while (n <= 1024) {
    bad = bad + sweep("seal", key, ctr, n);
    n = n + 1;
  }
  for m in [1279, 1280, 1281, 1471, 1472, 1473, 1535, 1536, 1537, 1983, 1984, 1985, 2047, 2048, 2049, 4095, 4096, 4097, 9000] {
    bad = bad + sweep("seal", key, ctr, m);
  }
  // The block counter wraps from 2^32 - 1 to 0 among the blocks of the text.
  let wrap: u8[] = ctr.with(0, 253 as u8).with(1, 255 as u8).with(2, 255 as u8).with(3, 255 as u8);
  bad = bad + sweep("wrap", key, wrap, 700);
  bad = bad + sweep("view", pattern(40, 9, 9)[3:35], pattern(20, 1, 2)[2:18], 283);
  if (bad > 99) {
    bad = 99;
  }
  return bad + shared(key, ctr);
}
`

// chachaPolyKernelSweep is the lengths the program seals after the lengths to
// 1024. The first group gives 448 bytes of text on x86-64 and 192 on arm64, and
// later groups are 512 and 256 bytes.
var chachaPolyKernelSweep = []int{1279, 1280, 1281, 1471, 1472, 1473, 1535, 1536, 1537, 1983, 1984, 1985, 2047, 2048, 2049, 4095, 4096, 4097, 9000}

// chachaPolySeal is RFC 8439's AEAD on a buffer: buf[at:at+n] is encrypted
// from block ctr + 1, and the tag of buf[aadAt:aadAt+aadN] and the ciphertext
// under the one-time key of block ctr goes to buf[at+n:at+n+16].
func chachaPolySeal(key, ctr, buf []byte, aadAt, aadN, at, n int) []byte {
	out := append([]byte(nil), buf...)
	next := append([]byte(nil), ctr...)
	binary.LittleEndian.PutUint32(next, binary.LittleEndian.Uint32(ctr)+1)
	copy(out[at:], chacha20Model(key, next, buf[at:at+n]))
	otk := chacha20Model(key, ctr, make([]byte, 32))
	padded := func(b []byte) []byte { return append(append([]byte(nil), b...), make([]byte, (16-len(b)%16)%16)...) }
	var lens [16]byte
	binary.LittleEndian.PutUint64(lens[:], uint64(aadN))
	binary.LittleEndian.PutUint64(lens[8:], uint64(n))
	msg := append(append(padded(out[aadAt:aadAt+aadN]), padded(out[at:at+n])...), lens[:]...)
	st := poly1305Model(append(make([]byte, 24), otk[:16]...), msg)
	h0, s0 := binary.LittleEndian.Uint64(st), binary.LittleEndian.Uint64(otk[16:])
	t0 := h0 + s0
	carry := uint64(0)
	if t0 < h0 {
		carry = 1
	}
	binary.LittleEndian.PutUint64(out[at+n:], t0)
	binary.LittleEndian.PutUint64(out[at+n+8:], binary.LittleEndian.Uint64(st[8:])+binary.LittleEndian.Uint64(otk[24:])+carry)
	return out
}

const rfc8439AEAD = "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116" +
	"1ae10b594f09e26a7e902ecbd0600691"

func chachaPolyKernelWant(t *testing.T) string {
	t.Helper()
	ctr, err := hex.DecodeString("00000000070000004041424344454647")
	if err != nil {
		t.Fatal(err)
	}
	aad, _ := hex.DecodeString("50515253c0c1c2c3c4c5c6c7")
	sunscreen := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	buf := append(append(append([]byte(nil), aad...), sunscreen...), make([]byte, 16)...)
	sealed := chachaPolySeal(chacha20Pattern(32, 1, 128), ctr, buf, 0, 12, 12, len(sunscreen))
	if got := hex.EncodeToString(sealed[12:]); got != rfc8439AEAD {
		t.Fatalf("the Go model gives %s for RFC 8439 2.8.2", got)
	}
	var b strings.Builder
	b.WriteString("rfc " + rfc8439AEAD + "\n")
	key := chacha20Pattern(32, 13, 5)
	base := chacha20Pattern(16, 7, 3)
	copy(base, []byte{0, 0, 0, 0})
	line := func(name string, key, ctr []byte, n int) {
		aadN := n % 23
		at := 3 + aadN
		fmt.Fprintf(&b, "%s %d %d\n", name, n, chacha20FNV(chachaPolySeal(key, ctr, chacha20Pattern(at+n+18, 3, 1), 3, aadN, at, n)))
	}
	for n := 0; n <= 1024; n++ {
		line("seal", key, base, n)
	}
	for _, n := range chachaPolyKernelSweep {
		line("seal", key, base, n)
	}
	line("wrap", key, append([]byte{253, 255, 255, 255}, base[4:]...), 700)
	line("view", chacha20Pattern(40, 9, 9)[3:35], chacha20Pattern(20, 1, 2)[2:18], 283)
	return b.String()
}

func chachaPolyKernelCheck(t *testing.T, got []byte, exit int) {
	t.Helper()
	gl, wl := strings.Split(string(got), "\n"), strings.Split(chachaPolyKernelWant(t), "\n")
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
	if exit != 0 {
		t.Fatalf("exited %d, want 0: a seal or an open differs from the two kernels, or a shared buffer was written in place (100)", exit)
	}
}

func chachaPolyRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally\n%s", errb.String())
	}
	chachaPolyKernelCheck(t, out.Bytes(), cmd.ProcessState.ExitCode())
}

func chachaPolySource(t *testing.T, prog string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "chachapoly.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostChaChaPolyKernelX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chachaPolySource(t, chachaPolyKernelProg)
	t.Run("in-process", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		chachaPolyRun(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("gas", func(t *testing.T) {
		chachaPolyRun(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
}

func TestSelfHostChaChaPolyKernelArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chachaPolySource(t, chachaPolyKernelProg)
	t.Run("in-process", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		chachaPolyRun(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)))
	})
	t.Run("gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		chachaPolyRun(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))))
	})
}

func TestSelfHostChaChaPolyKernelWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	chachaPolyRun(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, chachaPolySource(t, chachaPolyKernelProg), "FERN_STRICT_IR=1")))
}

func TestSelfHostInterpChaChaPolyKernel(t *testing.T) {
	if strings.HasPrefix(chachaPolyKernelProg, "import") {
		t.Fatal("the self-host interpreter driver runs single-file programs only")
	}
	t.Run("go", func(t *testing.T) {
		got, exit := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), chachaPolySource(t, chachaPolyKernelProg))
		chachaPolyKernelCheck(t, got, exit)
	})
	t.Run("self-host", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		cmd := runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run"))
		cmd.Stdin = strings.NewReader(chachaPolyKernelProg)
		chachaPolyRun(t, cmd)
	})
}

// TestSelfHostChaChaPolyKernelCensus runs the kernel program under the leak
// census on each target. Every allocation is freed: the copies that the kernels
// make of shared buffers, and on wasm the scratch box of the one-time key.
func TestSelfHostChaChaPolyKernelCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chachaPolySource(t, chachaPolyKernelProg)
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

// chachaPolyKernelMisuses each stop the program before a kernel reaches
// outside the buffer: a text and tag past its end, additional data past its
// end, a negative offset or length, a key that is not 32 bytes, and a counter
// block that is not 16 bytes.
var chachaPolyKernelMisuses = []string{
	"__chacha20_poly1305_seal_into(key, ctr, __alloc_u8(40), 0, 0, 4, 21)",
	"__chacha20_poly1305_open_into(key, ctr, __alloc_u8(40), 0, 0, 0, 25)",
	"__chacha20_poly1305_seal_into(key, ctr, __alloc_u8(40), 30, 11, 0, 4)",
	"__chacha20_poly1305_seal_into(key, ctr, __alloc_u8(40), 0 - 1, 2, 4, 4)",
	"__chacha20_poly1305_open_into(key, ctr, __alloc_u8(40), 0, 0 - 2, 4, 4)",
	"__chacha20_poly1305_seal_into(key, ctr, __alloc_u8(40), 0, 0, 0 - 1, 4)",
	"__chacha20_poly1305_seal_into(key, ctr, __alloc_u8(40), 0, 0, 0, 0 - 16)",
	"__chacha20_poly1305_seal_into(key[0:31], ctr, __alloc_u8(40), 0, 0, 0, 4)",
	"__chacha20_poly1305_open_into(__alloc_u8(33), ctr, __alloc_u8(40), 0, 0, 0, 4)",
	"__chacha20_poly1305_seal_into(key, ctr[0:12], __alloc_u8(40), 0, 0, 0, 4)",
}

func chachaPolyMisuseStops(t *testing.T, run func(src string) *exec.Cmd) {
	t.Helper()
	for _, call := range chachaPolyKernelMisuses {
		out, _ := run(chachaPolySource(t, chacha20KernelMisuseProg(call))).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostChaChaPolyKernelMisuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		chachaPolyMisuseStops(t, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src)) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		chachaPolyMisuseStops(t, func(src string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)) })
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		wasmtime := e2eharness.Wasmtime(t)
		chachaPolyMisuseStops(t, func(src string) *exec.Cmd {
			return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
		})
	})
	t.Run("interpreters", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		oracle := buildLangBinForInterp(t)
		for _, call := range chachaPolyKernelMisuses {
			prog := chacha20KernelMisuseProg(call)
			if got := interpExitStdin(t, oracle, prog, ""); got == 0 {
				t.Errorf("Go interpreter: %s returned instead of stopping the program", call)
			}
			if got := runDriverExit(t, runner, driver, []byte(prog)); got == 0 {
				t.Errorf("self-host interpreter: %s returned instead of stopping the program", call)
			}
		}
	})
}
