package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const aesGCMKernelsProg = `function hex(s: string): u8[] {
  let out: u8[] = __alloc_u8(s.len() / 2);
  let i: i32 = 0;
  while (i < out.len()) {
    out = out.with(i, (nibble(s[2 * i]) * 16 + nibble(s[2 * i + 1])) as u8);
    i = i + 1;
  }
  return out;
}

function nibble(c: u8): i32 {
  if (c >= b'a') {
    return c as i32 - 87;
  }
  return c as i32 - 48;
}

function pattern(n: i32, mul: i32, add: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i * mul + add) & 255) as u8);
    i = i + 1;
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

function schedules(): i32 {
  let k128: u8[] = hex("2b7e151628aed2a6abf7158809cf4f3c");
  let r128: u8[] = __aes_expand_key(k128);
  if (r128.len() != 176 || !same(r128[0:16], k128)) {
    return 1;
  }
  if (!same(r128[16:32], hex("a0fafe1788542cb123a339392a6c7605")) || !same(r128[160:176], hex("d014f9a8c9ee2589e13f0cc8b6630ca6"))) {
    return 2;
  }
  let k256: u8[] = hex("603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4");
  let r256: u8[] = __aes_expand_key(k256);
  if (r256.len() != 240 || !same(r256[0:32], k256)) {
    return 3;
  }
  if (!same(r256[32:48], hex("9ba354118e6925afa51a8b5f2067fcde")) || !same(r256[224:240], hex("fe4890d1e6188d0b046df344706c631e"))) {
    return 4;
  }
  if (__aes_expand_key(pattern(24, 1, 0)).len() != 0 || __aes_expand_key(pattern(15, 1, 0)).len() != 0) {
    return 5;
  }
  return 0;
}

function blocks(): i32 {
  let pt: u8[] = hex("00112233445566778899aabbccddeeff");
  let zero: u8[] = __alloc_u8(16);
  let r128: u8[] = __aes_expand_key(pattern(16, 1, 0));
  if (!same(__aes_ctr32(r128, pt, zero), hex("69c4e0d86a7b0430d8cdb78070b4c55a"))) {
    return 10;
  }
  let r256: u8[] = __aes_expand_key(pattern(32, 1, 0));
  if (!same(__aes_ctr32(r256, pt, zero), hex("8ea2b7ca516745bfeafc49904b496089"))) {
    return 11;
  }
  // The counter and the data as views: a slice and a string's bytes.
  if (!same(__aes_ctr32(r256[0:240], pt[0:16], "0123456789abcdef".as_bytes()), hex("be9385f965527388d2c528f2282d05ef"))) {
    return 12;
  }
  return 0;
}

function streams(): i32 {
  let r128: u8[] = __aes_expand_key(pattern(16, 1, 0));
  let r256: u8[] = __aes_expand_key(pattern(32, 1, 0));
  let wrap: u8[] = pattern(16, 3, 7).with(12, 255 as u8).with(13, 255 as u8).with(14, 255 as u8).with(15, 254 as u8);
  if (!same(__aes_ctr32(r128, wrap, pattern(50, 5, 1)), hex("` + aesKATCtr128Wrap + `"))) {
    return 20;
  }
  if (!same(__aes_ctr32(r128, pattern(16, 9, 4), pattern(300, 3, 2)), hex("` + aesKATCtr128Long + `"))) {
    return 21;
  }
  if (!same(__aes_ctr32(r256, pattern(16, 9, 4), pattern(300, 3, 2)), hex("` + aesKATCtr256Long + `"))) {
    return 22;
  }
  // Every length to 80 against the one-call answer, so each tail and each
  // stride boundary is a prefix of the long stream.
  let long: u8[] = __aes_ctr32(r128, pattern(16, 9, 4), pattern(300, 3, 2));
  let n: i32 = 0;
  while (n <= 80) {
    if (!same(__aes_ctr32(r128, pattern(16, 9, 4), pattern(n, 3, 2)), long[0:n])) {
      return 23;
    }
    n = n + 1;
  }
  if (__aes_ctr32(r128, wrap, __alloc_u8(0)).len() != 0) {
    return 24;
  }
  if (__aes_ctr32(r128[0:175], wrap, pattern(16, 1, 0)).len() != 0 || __aes_ctr32(r128, wrap[0:15], pattern(16, 1, 0)).len() != 0) {
    return 25;
  }
  return 0;
}

function hashes(): i32 {
  let h: u8[] = pattern(16, 29, 17);
  let y: u8[] = pattern(16, 31, 5);
  if (!same(__ghash(h, y, pattern(37, 7, 1)), hex("ac4fa968eac6d616d11d426ba3cdc567"))) {
    return 30;
  }
  if (!same(__ghash(h, y, pattern(300, 3, 2)), hex("ed2c9b0c2fdd2b6b2f702d85defbcc3c"))) {
    return 31;
  }
  if (!same(__ghash(h, y, pattern(16, 1, 0)), hex("dce773991bde281470e6973fe0bea5eb"))) {
    return 32;
  }
  // GCM test case 2: GHASH(H, {}, C) over the ciphertext and the lengths.
  let hh: u8[] = hex("66e94bd4ef8a2c3b884cfa59ca342b2e");
  let lens: u8[] = __alloc_u8(16).with(15, 128 as u8);
  let g: u8[] = __ghash(hh, __ghash(hh, __alloc_u8(16), hex("0388dace60b6a392f328c2b971b2fe78")), lens);
  if (!same(g, hex("f38cbb1ad69223dcc3457ae5b6b0f885"))) {
    return 33;
  }
  // No data leaves the state as it was; a short key or state answers empty.
  if (!same(__ghash(h, y, __alloc_u8(0)), y)) {
    return 34;
  }
  if (__ghash(h[0:15], y, y).len() != 0 || __ghash(h, y[0:15], y).len() != 0) {
    return 35;
  }
  // Chunked at block boundaries, the fold is the one-call fold.
  let data: u8[] = pattern(300, 3, 2);
  let part: u8[] = __ghash(h, __ghash(h, y, data[0:112]), data[112:300]);
  if (!same(part, hex("ed2c9b0c2fdd2b6b2f702d85defbcc3c"))) {
    return 36;
  }
  return 0;
}

function counter(nonce: [u8], last: u8): u8[] {
  let b: u8[] = __alloc_u8(16);
  let i: i32 = 0;
  while (i < 12) {
    b = b.with(i, nonce[i]);
    i = i + 1;
  }
  return b.with(15, last);
}

// folded is the nonce the in-place kernels build: iv with seq in its last eight bytes.
function folded(iv: [u8], seq: u64): u8[] {
  let out: u8[] = __alloc_u8(12);
  for i in 0..12 {
    out = out.with(i, iv[i]);
  }
  for k in 0..8 {
    out = out.with(4 + k, out[4 + k] ^ (seq >> (8 * (7 - k)) as u64 & 255 as u64) as u8);
  }
  return out;
}

// composed is GCM from the separate kernels: the data through CTR from J0 + 1
// and the tag over the ciphertext, which is data itself when opening.
function composed(rk: [u8], h: [u8], nonce: [u8], aad: [u8], data: [u8], seal: boolean): u8[] {
  let out: u8[] = __aes_ctr32(rk, counter(nonce, 2 as u8), data);
  let y: u8[] = __ghash(h, __alloc_u8(16), aad);
  if (seal) {
    y = __ghash(h, y, out);
  } else {
    y = __ghash(h, y, data);
  }
  let lens: u8[] = __alloc_u8(16).with(6, (aad.len() >> 5) as u8).with(7, (aad.len() << 3 & 255) as u8)
    .with(14, (data.len() >> 5) as u8).with(15, (data.len() << 3 & 255) as u8);
  y = __ghash(h, y, lens);
  let tag: u8[] = __aes_ctr32(rk, counter(nonce, 1 as u8), y);
  let all: u8[] = __alloc_u8(out.len() + 16);
  let i: i32 = 0;
  while (i < all.len()) {
    if (i < out.len()) {
      all = all.with(i, out[i]);
    } else {
      all = all.with(i, tag[i - out.len()]);
    }
    i = i + 1;
  }
  return all;
}

function put(own buf: u8[], at: i32, src: [u8]): u8[] {
  for i in 0..src.len() {
    buf = buf.with(at + i, src[i]);
  }
  return buf;
}

// laid is [filler][aad][data][16 for the tag][filler], the aad at 3.
function laid(aad: [u8], data: [u8]): u8[] {
  return put(put(pattern(3 + aad.len() + data.len() + 16 + 5, 11, 5), 3, aad), 3 + aad.len(), data);
}

function flipped(buf: [u8], at: i32): u8[] {
  return put(pattern(buf.len(), 0, 0), 0, buf).with(at, buf[at] ^ 1 as u8);
}

// Seals and opens GCM test cases 4 and 16 in place, the aad ahead of the text, and refuses each tampered byte.
function vectors(rk: [u8], h: [u8], want: string, base: i32): i32 {
  let iv: u8[] = hex("cafebabefacedbaddecaf888");
  let aad: u8[] = hex("feedfacedeadbeeffeedfacedeadbeefabaddad2");
  let pt: u8[] = hex("` + gcmCase4PT + `");
  let sealed: u8[] = __aes_gcm_seal_into(rk, h, iv, 0 as u64, laid(aad, pt), 3, 20, 23, 60);
  if (!same(sealed[23:99], hex(want)) || !same(sealed[3:23], aad) || !same(sealed[0:3], pattern(3, 11, 5)) || !same(sealed[99:104], pattern(104, 11, 5)[99:104])) {
    return base;
  }
  let opened: u8[] = __aes_gcm_open_into(rk, h, iv, 0 as u64, sealed, 3, 20, 23, 60);
  if (!same(opened[23:83], pt) || opened[83] != 0 || !same(opened[84:99], hex(want)[61:76])) {
    return base + 1;
  }
  // A changed tag, ciphertext or aad byte, or the wrong sequence number, is refused with exactly 1.
  let refused: i32[] = [98, 83, 84, 23, 50, 82, 3, 22];
  for at in refused {
    let o: u8[] = __aes_gcm_open_into(rk, h, iv, 0 as u64, flipped(put(laid(aad, pt), 23, hex(want)), at), 3, 20, 23, 60);
    if (o[83] != 1) {
      return base + 2;
    }
  }
  if (__aes_gcm_open_into(rk, h, iv, 1 as u64, put(laid(aad, pt), 23, hex(want)), 3, 20, 23, 60)[83] != 1) {
    return base + 3;
  }
  return 0;
}

function fused(): i32 {
  let rk: u8[] = __aes_expand_key(hex("feffe9928665731c6d6a8f9467308308"));
  let h: u8[] = __aes_ctr32(rk, __alloc_u8(16), __alloc_u8(16));
  let r: i32 = vectors(rk, h, "` + gcmCase4Sealed + `", 40);
  if (r != 0) {
    return r;
  }
  let k16: u8[] = __aes_expand_key(hex("feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308"));
  r = vectors(k16, __aes_ctr32(k16, __alloc_u8(16), __alloc_u8(16)), "` + gcmCase16Sealed + `", 50);
  if (r != 0) {
    return r;
  }
  // Every length to 80 under both key sizes, the aad's length and the sequence number varying, the aad after
  // the tag and the iv a string's bytes.
  let r256: u8[] = __aes_expand_key(pattern(32, 7, 3));
  let h256: u8[] = __aes_ctr32(r256, __alloc_u8(16), __alloc_u8(16));
  let iv: [u8] = "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c".as_bytes();
  for n in 0..81 {
    let data: u8[] = pattern(n, 3, 2);
    let a: u8[] = pattern(n * 7 % 37, 5, 1);
    let seq: u64 = n as u64 * 0x0102030405060709 as u64;
    let nonce: u8[] = folded(iv, seq);
    let buf: u8[] = put(put(pattern(n + 16 + a.len(), 0, 0), 0, data), n + 16, a);
    let s128: u8[] = __aes_gcm_seal_into(rk, h, iv, seq, buf, n + 16, a.len(), 0, n);
    if (!same(s128[0:n + 16], composed(rk, h, nonce, a, data, true))) {
      return 60;
    }
    let o128: u8[] = __aes_gcm_open_into(rk, h, iv, seq, s128, n + 16, a.len(), 0, n);
    if (!same(o128[0:n], data) || o128[n] != 0) {
      return 61;
    }
    let s256: u8[] = __aes_gcm_seal_into(r256, h256, iv, seq, put(put(pattern(n + 16 + a.len(), 0, 0), 0, data), n + 16, a), n + 16, a.len(), 0, n);
    if (!same(s256[0:n + 16], composed(r256, h256, nonce, a, data, true))) {
      return 62;
    }
    let o256: u8[] = __aes_gcm_open_into(r256, h256, iv, seq, s256, n + 16, a.len(), 0, n);
    if (!same(o256[0:n], data) || o256[n] != 0) {
      return 63;
    }
  }
  return shared();
}

// A buffer with another holder is copied before it is sealed, so the holder still reads the plaintext; a
// literal's storage is never written.
function shared(): i32 {
  let rk: u8[] = __aes_expand_key(pattern(16, 1, 0));
  let h: u8[] = __aes_ctr32(rk, __alloc_u8(16), __alloc_u8(16));
  let iv: u8[] = pattern(12, 1, 9);
  let buf: u8[] = pattern(40, 3, 1);
  let kept: u8[] = buf;
  let sealed: u8[] = __aes_gcm_seal_into(rk, h, iv, 0 as u64, buf, 0, 4, 4, 20);
  if (!same(kept, pattern(40, 3, 1)) || !same(sealed[4:40], composed(rk, h, iv, pattern(40, 3, 1)[0:4], pattern(40, 3, 1)[4:24], true))) {
    return 70;
  }
  let lit: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20];
  let once: u8[] = __aes_gcm_seal_into(rk, h, iv, 0 as u64, lit, 0, 0, 0, 4);
  let again: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20];
  if (!same(lit, pattern(20, 1, 1)) || !same(again, pattern(20, 1, 1)) || same(once[0:4], lit[0:4])) {
    return 71;
  }
  return 0;
}

function main(): i32 {
  let r: i32 = schedules();
  if (r == 0) {
    r = blocks();
  }
  if (r == 0) {
    r = streams();
  }
  if (r == 0) {
    r = hashes();
  }
  if (r == 0) {
    r = fused();
  }
  return r;
}
`

const (
	gcmCase4PT       = "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39"
	gcmCase4Sealed   = "42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e0915bc94fbc3221a5db94fae95ae7121a47"
	gcmCase16Sealed  = "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f66276fc6ece0f4e1768cddf8853bb2d551b"
	aesKATCtr128Wrap = "5261f7439cb239734ad239c63d4ed9a38c514451afce7c8b7a847f7e8e38ec52ceacb4c7ff57d15ca56b37375774a824bc59"
	aesKATCtr128Long = "5922f813dfd110fade6b86b5239d7b3efed6fc1ffd3bfd19816366e23957aa2c1e1b972a9663e913c333111c917ef4c9a8ae6a32805e52da1b6fb69a5b3ae0aca7367f5c57691c8cad91b5d83a43739d9b02de52c045762a1853d844f1d36666c16793d96a129bc98fb30bdedf5008fd6aa1f0aef3905e6c9d690374d630ec09ecb0beccd634057885bcb40a82fe779696c44ee629a9c3e617e8a23b22ab483722f5e4648e131f9ede3a5c22b5edf44ff57c45db32c54425105e49e5c297b11c29c62cd3fa07525f4a9bd9655faf3bbdab3c8323279f9a9643f8e234a34452a7cbcf2fc415f584eb3a741817ac063bf17b5c7f5f02a89f54e1bdbde3963b7461f9714476506125f07e7a9aa76c4e7a8f702de5437ec8c518c8c5398019107165754ec2ae207c1d881f1a16bd"
	aesKATCtr256Long = "ff56feaa6d804759f03c320d54b0ddaa1f861681450c6b175d0bf7f08913563e8fb24b5317c376b527ea56bf644e1890139ed3b31ffbce0eb48c10396bf2f33a08d1895094b7388d0802f7224bc2a24fe30b0fe6d514b72e813b9e0d8462263082317bc6785a570d1c00a1756f44e658e6cac967bf2e9e5f5f20d582d96dd521292abc33779b235d7513e4a49de50a8ea380f3a8b4495523d698980ae697ed71c112dd0734e3b85c507624994d088c42dbc7050fe998396518ace8f6905be9bf659b3773856472d6586f3b40a67aeea10d7d98c8d952c350dd6e634af5ee02b34f79627149f82ed8c1ec842f77036162afbe7cda134c625858dbde52daf1e5f1147b3c5228a42b20281b0640978addab460fc9f29045f9aeacd21d9ae1554957c78b383970297a22e231414e"
)

// aesGCMIntoMisuses each stop the program instead of reaching outside the
// buffer: a text region past its end, a negative offset, an aad region past
// its end, an iv that is not 12 bytes, a schedule short of AES-128's.
var aesGCMIntoMisuses = []string{
	"__aes_gcm_seal_into(rk, h, iv, 0 as u64, __alloc_u8(20), 0, 0, 0, 8)",
	"__aes_gcm_open_into(rk, h, iv, 0 as u64, __alloc_u8(40), 0, 4, 0 - 1, 8)",
	"__aes_gcm_seal_into(rk, h, iv, 0 as u64, __alloc_u8(40), 30, 11, 0, 8)",
	"__aes_gcm_seal_into(rk, h, iv[0:11], 0 as u64, __alloc_u8(40), 0, 0, 0, 8)",
	"__aes_gcm_open_into(rk[0:175], h, iv, 0 as u64, __alloc_u8(40), 0, 0, 0, 8)",
}

func aesGCMIntoMisuseProg(call string) string {
	return `function main(): i32 {
  let rk: u8[] = __aes_expand_key(__alloc_u8(16));
  let h: u8[] = __aes_ctr32(rk, __alloc_u8(16), __alloc_u8(16));
  let iv: u8[] = __alloc_u8(12);
  let out: u8[] = ` + call + `;
  print("survived");
  return out.len() - out.len();
}
`
}

func aesGCMKernelsCompile(t *testing.T, target string) string {
	t.Helper()
	return aesGCMCompile(t, target, aesGCMKernelsProg)
}

func aesGCMCompile(t *testing.T, target, prog string) string {
	t.Helper()
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "kernels.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "kernels")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-o", bin, src, cli.stdlib)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI -target %s: %v\n%s", target, err, msg)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestSelfHostAESGCMKernelsX86_64(t *testing.T) {
	bin := aesGCMKernelsCompile(t, "x86-64-linux")
	if code, stderr := exitOf(t, exec.Command(bin)); code != 0 {
		t.Fatalf("exited %d, want 0 (see aesGCMKernelsProg for what each code means)\n%s", code, stderr)
	}
}

func aesGCMMisuseStops(t *testing.T, run func(prog string) *exec.Cmd) {
	t.Helper()
	for _, call := range aesGCMIntoMisuses {
		out, _ := run(aesGCMIntoMisuseProg(call)).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostAESGCMKernelsMisuseX86_64(t *testing.T) {
	aesGCMMisuseStops(t, func(prog string) *exec.Cmd { return exec.Command(aesGCMCompile(t, "x86-64-linux", prog)) })
}

func TestSelfHostAESGCMKernelsMisuseArm64(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	aesGCMMisuseStops(t, func(prog string) *exec.Cmd {
		return e2eharness.RunArm64Bin(qemu, aesGCMCompile(t, "arm64-linux", prog))
	})
}

func TestSelfHostAESGCMKernelsMisuseWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	aesGCMMisuseStops(t, func(prog string) *exec.Cmd {
		src := filepath.Join(t.TempDir(), "misuse.fern")
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
	})
}

func TestSelfHostAESGCMKernelsArm64(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := aesGCMKernelsCompile(t, "arm64-linux")
	if code, stderr := exitOf(t, e2eharness.RunArm64Bin(qemu, bin)); code != 0 {
		t.Fatalf("exited %d, want 0 (see aesGCMKernelsProg for what each code means)\n%s", code, stderr)
	}
}

func TestSelfHostAESGCMKernelsWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "kernels.fern")
	if err := os.WriteFile(src, []byte(aesGCMKernelsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr := exitOf(t, exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))); code != 0 {
		t.Fatalf("wasmtime run exited %d, want 0 (see aesGCMKernelsProg for what each code means)\n%s", code, stderr)
	}
}

func TestSelfHostInterpAESGCMKernels(t *testing.T) {
	if strings.HasPrefix(aesGCMKernelsProg, "import") {
		t.Fatal("the self-host interpreter driver runs single-file programs only")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	gcc, runner := x86_64Tooling(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
	oracle := buildLangBinForInterp(t)
	if got := interpExitStdin(t, oracle, aesGCMKernelsProg, ""); got != 0 {
		t.Fatalf("Go interpreter exited %d, want 0", got)
	}
	if got := runDriverExit(t, runner, driver, []byte(aesGCMKernelsProg)); got != 0 {
		t.Fatalf("self-host interpreter exited %d, want 0", got)
	}
	for _, call := range aesGCMIntoMisuses {
		prog := aesGCMIntoMisuseProg(call)
		if got := interpExitStdin(t, oracle, prog, ""); got == 0 {
			t.Errorf("Go interpreter: %s returned instead of stopping the program", call)
		}
		if got := runDriverExit(t, runner, driver, []byte(prog)); got == 0 {
			t.Errorf("self-host interpreter: %s returned instead of stopping the program", call)
		}
	}
}
