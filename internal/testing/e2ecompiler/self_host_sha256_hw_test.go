package e2ecompiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// std/crypto's SHA-256 takes __sha256_hw_blocks, the SHA256H/H2/SU0/SU1
// kernel, where __sha256_hw says the target has the instructions (arm64), and
// its own rounds elsewhere. The program prints which path it took, then
// digests that cover every padding edge (each length to 200 bytes, so 55, 56,
// 63, 64, 119 and 120 among them) for SHA-256 and SHA-224, a 1000-byte input
// fed in chunks that straddle the block, a 1 MiB input, and the kernel called
// directly: three blocks from an unaligned slice with a tail it must ignore,
// fewer bytes than a block, a short state, and a string's bytes. Go's
// crypto/sha256 and sha256Block below give the expected text.
const sha256HWProg = `import "std/crypto";

function digits(): string[] {
  return ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"];
}

function hex(b: u8[]): string {
  let d: string[] = digits();
  let out: string = "";
  for x in b {
    out = out + d[x as i32 / 16] + d[x as i32 % 16];
  }
  return out;
}

function pattern(n: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i * 7 + 3) & 255) as u8);
    i = i + 1;
  }
  return out;
}

function iv(): u8[] {
  let words: u32[] = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
  let out: u8[] = __alloc_u8(32);
  let i: i32 = 0;
  while (i < 32) {
    out = out.with(i, (words[i / 4] >> (8 * (i % 4)) as u32 & 255) as u8);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  if (__sha256_hw()) {
    print("hw=1");
  } else {
    print("hw=0");
  }
  let n: i32 = 0;
  while (n <= 200) {
    let p: u8[] = pattern(n);
    print(crypto.sha256_new().update_array(p).final_hex());
    print(crypto.sha224_new().update_array(p).final_hex());
    n = n + 1;
  }
  let big: u8[] = pattern(1000);
  for c in [1, 7, 63, 64, 65, 200] {
    let h: crypto.Sha256 = crypto.sha256_new();
    let at: i32 = 0;
    while (at < 1000) {
      let end: i32 = at + c;
      if (end > 1000) {
        end = 1000;
      }
      h = h.update_bytes(big[at:end]);
      at = end;
    }
    print(h.final_hex());
  }
  print(crypto.sha256_new().update_array(pattern(1048576)).final_hex());
  let st: u8[] = iv();
  print("k=" + hex(__sha256_hw_blocks(st, big[3:205])));
  print("k=" + hex(__sha256_hw_blocks(st, big[0:63])));
  print("k=" + hex(__sha256_hw_blocks(st[0:31], big)));
  print("k=" + hex(__sha256_hw_blocks(st, "The quick brown fox jumps over the lazy dog, then over the lazy cat.".as_bytes())));
  return 0;
}
`

var sha256K = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

// sha256Block is FIPS 180-4's compression of one block into h.
func sha256Block(h *[8]uint32, p []byte) {
	var w [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(p[4*i:])
	}
	for i := 16; i < 64; i++ {
		s0 := bits.RotateLeft32(w[i-15], -7) ^ bits.RotateLeft32(w[i-15], -18) ^ w[i-15]>>3
		s1 := bits.RotateLeft32(w[i-2], -17) ^ bits.RotateLeft32(w[i-2], -19) ^ w[i-2]>>10
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}
	a, b, c, d, e, f, g, hh := h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]
	for i := 0; i < 64; i++ {
		t1 := hh + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + (e&f ^ ^e&g) + sha256K[i] + w[i]
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + (a&b ^ a&c ^ b&c)
		a, b, c, d, e, f, g, hh = t1+t2, a, b, c, d+t1, e, f, g
	}
	for i, v := range [8]uint32{a, b, c, d, e, f, g, hh} {
		h[i] += v
	}
}

// sha256HWKernel is what __sha256_hw_blocks answers where the instructions
// exist: the IV with data's whole blocks folded in, as little-endian words.
func sha256HWKernel(data []byte) string {
	h := [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}
	for len(data) >= 64 {
		sha256Block(&h, data[:64])
		data = data[64:]
	}
	out := make([]byte, 32)
	for i, v := range h {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return hex.EncodeToString(out)
}

func sha256HWPattern(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i*7 + 3)
	}
	return out
}

// sha256HWWant is the program's expected output; hw says whether the target
// has the instructions, which decides what the direct kernel calls answer.
func sha256HWWant(hw bool) string {
	var b strings.Builder
	if hw {
		b.WriteString("hw=1\n")
	} else {
		b.WriteString("hw=0\n")
	}
	for n := 0; n <= 200; n++ {
		p := sha256HWPattern(n)
		fmt.Fprintf(&b, "%x\n%x\n", sha256.Sum256(p), sha256.Sum224(p))
	}
	big := sha256HWPattern(1000)
	for range []int{1, 7, 63, 64, 65, 200} {
		fmt.Fprintf(&b, "%x\n", sha256.Sum256(big))
	}
	fmt.Fprintf(&b, "%x\n", sha256.Sum256(sha256HWPattern(1048576)))
	kernel := []string{"", "", "", ""}
	if hw {
		kernel = []string{
			sha256HWKernel(big[3:205]),
			sha256HWKernel(big[0:63]),
			"",
			sha256HWKernel([]byte("The quick brown fox jumps over the lazy dog, then over the lazy cat.")),
		}
	}
	for _, k := range kernel {
		b.WriteString("k=" + k + "\n")
	}
	return b.String()
}

func sha256HWCheck(t *testing.T, cmd *exec.Cmd, hw bool) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, errb.String())
	}
	if got, want := out.String(), sha256HWWant(hw); got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := range wl {
			if i >= len(gl) || gl[i] != wl[i] {
				g := "(missing)"
				if i < len(gl) {
					g = gl[i]
				}
				t.Fatalf("line %d: got %q, want %q", i+1, g, wl[i])
			}
		}
		t.Fatalf("output has %d lines, want %d", len(gl), len(wl))
	}
}

func sha256HWSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "sha.fern")
	if err := os.WriteFile(src, []byte(sha256HWProg), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestSelfHostSHA256HW runs the program on each Linux target: arm64 under
// qemu through both the self-host assembler and GNU as, which take the
// instructions, and x86-64 and wasm, which run std/crypto's rounds.
func TestSelfHostSHA256HW(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := sha256HWSource(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		sha256HWCheck(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), false)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		sha256HWCheck(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)), true)
	})
	t.Run("arm64-linux-gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		sha256HWCheck(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))), true)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		sha256HWCheck(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")), false)
	})
}

func TestSelfHostArm64DarwinSHA256HW(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	bin := filepath.Join(t.TempDir(), "sha")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, sha256HWSource(t), e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	sha256HWCheck(t, exec.Command(bin), true)
}
