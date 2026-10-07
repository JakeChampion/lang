package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The three AES-GCM kernels std/crypto/aes_gcm is built on, each checked on
// its own against known answers on every target: AES-NI and PCLMULQDQ on
// x86-64, AESE and PMULL on arm64 (under qemu), the bitsliced AES and the
// masked carry-less multiply on wasm, and the two interpreters' reference
// spellings.
//
// The key schedules are FIPS-197 Appendix A.1 and A.3, the blocks Appendix
// C.1 and C.3, and the GHASH pin GCM's test case 2. The rest were computed
// by Go's crypto/aes and a reference GHASH checked against crypto/cipher's
// GCM: a counter whose low word wraps, partial last blocks, and lengths that
// cross every unrolled stride. Each failure answers its own code; 0 means
// every comparison matched.
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
  // The counter and the data as views: a string's bytes and a slice.
  if (!same(__aes_ctr32(r256[0:240], "\x00\x11\x22\x33\x44\x55\x66\x77\x88\x99\xaa\xbb\xcc\xdd\xee\xff".as_bytes(), zero[0:16]), hex("8ea2b7ca516745bfeafc49904b496089"))) {
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
  return r;
}
`

const (
	aesKATCtr128Wrap = "5261f7439cb239734ad239c63d4ed9a38c514451afce7c8b7a847f7e8e38ec52ceacb4c7ff57d15ca56b37375774a824bc59"
	aesKATCtr128Long = "5922f813dfd110fade6b86b5239d7b3efed6fc1ffd3bfd19816366e23957aa2c1e1b972a9663e913c333111c917ef4c9a8ae6a32805e52da1b6fb69a5b3ae0aca7367f5c57691c8cad91b5d83a43739d9b02de52c045762a1853d844f1d36666c16793d96a129bc98fb30bdedf5008fd6aa1f0aef3905e6c9d690374d630ec09ecb0beccd634057885bcb40a82fe779696c44ee629a9c3e617e8a23b22ab483722f5e4648e131f9ede3a5c22b5edf44ff57c45db32c54425105e49e5c297b11c29c62cd3fa07525f4a9bd9655faf3bbdab3c8323279f9a9643f8e234a34452a7cbcf2fc415f584eb3a741817ac063bf17b5c7f5f02a89f54e1bdbde3963b7461f9714476506125f07e7a9aa76c4e7a8f702de5437ec8c518c8c5398019107165754ec2ae207c1d881f1a16bd"
	aesKATCtr256Long = "ff56feaa6d804759f03c320d54b0ddaa1f861681450c6b175d0bf7f08913563e8fb24b5317c376b527ea56bf644e1890139ed3b31ffbce0eb48c10396bf2f33a08d1895094b7388d0802f7224bc2a24fe30b0fe6d514b72e813b9e0d8462263082317bc6785a570d1c00a1756f44e658e6cac967bf2e9e5f5f20d582d96dd521292abc33779b235d7513e4a49de50a8ea380f3a8b4495523d698980ae697ed71c112dd0734e3b85c507624994d088c42dbc7050fe998396518ace8f6905be9bf659b3773856472d6586f3b40a67aeea10d7d98c8d952c350dd6e634af5ee02b34f79627149f82ed8c1ec842f77036162afbe7cda134c625858dbde52daf1e5f1147b3c5228a42b20281b0640978addab460fc9f29045f9aeacd21d9ae1554957c78b383970297a22e231414e"
)

// aesGCMKernelsCompile compiles the program for target with the self-host CLI.
func aesGCMKernelsCompile(t *testing.T, target string) string {
	t.Helper()
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "kernels.fern")
	if err := os.WriteFile(src, []byte(aesGCMKernelsProg), 0o644); err != nil {
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
}
