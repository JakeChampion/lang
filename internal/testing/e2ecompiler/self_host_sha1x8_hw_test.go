package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const sha1x8HWProg = `function digits(): string[] {
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

function pattern(n: i32, mul: i32, add: i32): u8[] {
  let out: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    out = out.with(i, ((i * mul + add) & 255) as u8);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  if (__sha1x8_hw()) {
    print("hw=1");
  } else {
    print("hw=0");
  }
  let st: u8[] = pattern(160, 13, 5);
  let big: u8[] = pattern(2000, 7, 3);
  print("k=" + hex(__sha1x8_hw_blocks(st, big[0:0])));
  print("k=" + hex(__sha1x8_hw_blocks(st, big[0:512])));
  print("k=" + hex(__sha1x8_hw_blocks(st, big[7:1543])));
  print("k=" + hex(__sha1x8_hw_blocks(st, big[0:1000])));
  print("k=" + hex(__sha1x8_hw_blocks(st[0:159], big)));
  return 0;
}
`

func sha1x8Pattern(n, mul, add int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*mul + add) & 255)
	}
	return out
}

// sha1x8Kernel runs each lane of a word-major state over its blocks: lane k
// of group g is at byte 512*g + 64*k.
func sha1x8Kernel(st, data []byte) string {
	var lanes [8][5]uint32
	for k := 0; k < 8; k++ {
		for w := 0; w < 5; w++ {
			lanes[k][w] = binary.LittleEndian.Uint32(st[4*(8*w+k):])
		}
	}
	for g := 0; g+512 <= len(data); g += 512 {
		for k := 0; k < 8; k++ {
			sha1Block(&lanes[k], data[g+64*k:g+64*k+64])
		}
	}
	out := make([]byte, 160)
	for k := 0; k < 8; k++ {
		for w := 0; w < 5; w++ {
			binary.LittleEndian.PutUint32(out[4*(8*w+k):], lanes[k][w])
		}
	}
	return hex.EncodeToString(out)
}

func sha1x8HWWant(hw bool) string {
	var b strings.Builder
	kernel := []string{"", "", "", "", ""}
	if hw {
		b.WriteString("hw=1\n")
		st, big := sha1x8Pattern(160, 13, 5), sha1x8Pattern(2000, 7, 3)
		kernel = []string{
			sha1x8Kernel(st, nil),
			sha1x8Kernel(st, big[0:512]),
			sha1x8Kernel(st, big[7:1543]),
			sha1x8Kernel(st, big[0:1000]),
			"",
		}
	} else {
		b.WriteString("hw=0\n")
	}
	for _, k := range kernel {
		b.WriteString("k=" + k + "\n")
	}
	return b.String()
}

func sha1x8HWCheck(t *testing.T, cmd *exec.Cmd, hw bool) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, errb.String())
	}
	if got, want := out.String(), sha1x8HWWant(hw); got != want {
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

func TestSelfHostSHA1x8HW(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "sha1x8.fern")
	if err := os.WriteFile(src, []byte(sha1x8HWProg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux-gas", func(t *testing.T) {
		sha1x8HWCheck(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), true)
	})
	t.Run("x86-64-linux", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		sha1x8HWCheck(t, runX86_64Bin(cli.runner, bin), true)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		sha1x8HWCheck(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)), true)
	})
	t.Run("arm64-linux-gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		sha1x8HWCheck(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))), true)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		sha1x8HWCheck(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")), false)
	})
}
