package e2ecompiler

import (
	"bytes"
	"crypto/sha1"
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

const sha1HWProg = `import "std/crypto";

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
  let words: u32[] = [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0];
  let out: u8[] = __alloc_u8(20);
  let i: i32 = 0;
  while (i < 20) {
    out = out.with(i, (words[i / 4] >> (8 * (i % 4)) as u32 & 255) as u8);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  if (__sha1_hw()) {
    print("hw=1");
  } else {
    print("hw=0");
  }
  let n: i32 = 0;
  while (n <= 200) {
    print(hex(crypto.sha1_new().update(pattern(n)).finish()));
    n = n + 1;
  }
  let big: u8[] = pattern(1000);
  for c in [1, 7, 63, 64, 65, 200] {
    let h: crypto.Sha1 = crypto.sha1_new();
    let at: i32 = 0;
    while (at < 1000) {
      let end: i32 = at + c;
      if (end > 1000) {
        end = 1000;
      }
      h = h.update(big[at:end]);
      at = end;
    }
    print(hex(h.finish()));
  }
  print(hex(crypto.sha1_new().update(pattern(1048576)).finish()));
  let st: u8[] = iv();
  print("k=" + hex(__sha1_hw_blocks(st, big[3:205])));
  print("k=" + hex(__sha1_hw_blocks(st, big[0:63])));
  print("k=" + hex(__sha1_hw_blocks(st[0:19], big)));
  print("k=" + hex(__sha1_hw_blocks(st, "The quick brown fox jumps over the lazy dog, then over the lazy cat.".as_bytes())));
  return 0;
}
`

func sha1Block(h *[5]uint32, p []byte) {
	var w [80]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(p[4*i:])
	}
	for i := 16; i < 80; i++ {
		w[i] = bits.RotateLeft32(w[i-3]^w[i-8]^w[i-14]^w[i-16], 1)
	}
	a, b, c, d, e := h[0], h[1], h[2], h[3], h[4]
	for i := 0; i < 80; i++ {
		var f, k uint32
		switch {
		case i < 20:
			f, k = b&c|^b&d, 0x5a827999
		case i < 40:
			f, k = b^c^d, 0x6ed9eba1
		case i < 60:
			f, k = b&c|b&d|c&d, 0x8f1bbcdc
		default:
			f, k = b^c^d, 0xca62c1d6
		}
		t := bits.RotateLeft32(a, 5) + f + e + k + w[i]
		a, b, c, d, e = t, a, bits.RotateLeft32(b, 30), c, d
	}
	for i, v := range [5]uint32{a, b, c, d, e} {
		h[i] += v
	}
}

func sha1HWKernel(data []byte) string {
	h := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	for len(data) >= 64 {
		sha1Block(&h, data[:64])
		data = data[64:]
	}
	out := make([]byte, 20)
	for i, v := range h {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return hex.EncodeToString(out)
}

func sha1HWWant(hw bool) string {
	var b strings.Builder
	if hw {
		b.WriteString("hw=1\n")
	} else {
		b.WriteString("hw=0\n")
	}
	for n := 0; n <= 200; n++ {
		fmt.Fprintf(&b, "%x\n", sha1.Sum(sha256HWPattern(n)))
	}
	big := sha256HWPattern(1000)
	for range []int{1, 7, 63, 64, 65, 200} {
		fmt.Fprintf(&b, "%x\n", sha1.Sum(big))
	}
	fmt.Fprintf(&b, "%x\n", sha1.Sum(sha256HWPattern(1048576)))
	kernel := []string{"", "", "", ""}
	if hw {
		kernel = []string{
			sha1HWKernel(big[3:205]),
			sha1HWKernel(big[0:63]),
			"",
			sha1HWKernel([]byte("The quick brown fox jumps over the lazy dog, then over the lazy cat.")),
		}
	}
	for _, k := range kernel {
		b.WriteString("k=" + k + "\n")
	}
	return b.String()
}

func sha1HWCheck(t *testing.T, cmd *exec.Cmd, hw bool) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, errb.String())
	}
	if got, want := out.String(), sha1HWWant(hw); got != want {
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

func TestSelfHostSHA1HW(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "sha1.fern")
	if err := os.WriteFile(src, []byte(sha1HWProg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		sha1HWCheck(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), false)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		sha1HWCheck(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)), true)
	})
	t.Run("arm64-linux-gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		sha1HWCheck(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))), true)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		sha1HWCheck(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")), false)
	})
}

func TestSelfHostArm64DarwinSHA1HW(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := filepath.Join(t.TempDir(), "sha1.fern")
	if err := os.WriteFile(src, []byte(sha1HWProg), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "sha1")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	sha1HWCheck(t, exec.Command(bin), true)
}
