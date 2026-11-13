package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// chacha20KernelProg prints RFC 8439's block and cipher answers through
// __chacha20_xor_into. Then it prints an FNV-1a digest of the whole buffer for
// each length to 1024 and for lengths around the groups of four and eight
// blocks, with the text 5 bytes into the buffer. A counter whose low word wraps,
// and views for the key and the counter, come last. The exit code is not 0 when
// a buffer with another holder, or a literal, is written in place.
const chacha20KernelProg = `function pattern(n: i32, mul: i32, add: i32): u8[] {
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

function hex(b: u8[]): string {
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

function sweep(key: u8[], ctr: u8[], n: i32): void {
  let out: u8[] = __chacha20_xor_into(key, ctr, pattern(n + 9, 3, 1), 5, n);
  print(n.to_string() + " " + fnv(out).to_string());
}

// A buffer with another holder is copied before the kernel writes it, and a literal's storage is never written.
function shared(key: u8[], ctr: u8[]): i32 {
  let buf: u8[] = pattern(40, 3, 1);
  let kept: u8[] = buf;
  let out: u8[] = __chacha20_xor_into(key, ctr, buf, 4, 20);
  if (!same(kept, pattern(40, 3, 1)) || same(out, kept) || !same(out[0:4], kept[0:4]) || !same(out[24:40], kept[24:40])) {
    return 1;
  }
  let lit: u8[] = [1, 2, 3, 4, 5, 6, 7, 8];
  let once: u8[] = __chacha20_xor_into(key, ctr, lit, 0, 8);
  let again: u8[] = [1, 2, 3, 4, 5, 6, 7, 8];
  if (!same(lit, again) || same(once, lit)) {
    return 2;
  }
  // XOR twice gives the text back.
  let back: u8[] = __chacha20_xor_into(key, ctr, __chacha20_xor_into(key, ctr, pattern(777, 11, 2), 0, 777), 0, 777);
  if (!same(back, pattern(777, 11, 2))) {
    return 3;
  }
  return 0;
}

function main(): i32 {
  // RFC 8439 2.3.2 and 2.4.2.
  let k: u8[] = pattern(32, 1, 0);
  print("block " + hex(__chacha20_xor_into(k, unhex("01000000000000090000004a00000000"), __alloc_u8(64), 0, 64)));
  let sunscreen: [u8] = "Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.".as_bytes();
  let text: u8[] = __alloc_u8(sunscreen.len());
  let i: i32 = 0;
  while (i < text.len()) {
    text = text.with(i, sunscreen[i]);
    i = i + 1;
  }
  print("cipher " + hex(__chacha20_xor_into(k, unhex("01000000000000000000004a00000000"), text, 0, text.len())));
  let key: u8[] = pattern(32, 13, 5);
  let ctr: u8[] = pattern(16, 7, 3);
  let n: i32 = 0;
  while (n <= 1024) {
    sweep(key, ctr, n);
    n = n + 1;
  }
  for m in [1279, 1280, 1281, 1535, 1536, 1537, 2047, 2048, 2049, 4095, 4096, 4097, 9000] {
    sweep(key, ctr, m);
  }
  // The block counter wraps from 2^32 - 1 to 0 inside a group of blocks.
  let wrap: u8[] = ctr.with(0, 253 as u8).with(1, 255 as u8).with(2, 255 as u8).with(3, 255 as u8);
  print("wrap " + fnv(__chacha20_xor_into(key, wrap, pattern(700, 5, 7), 0, 700)).to_string());
  print("view " + fnv(__chacha20_xor_into(pattern(40, 9, 9)[3:35], pattern(20, 1, 2)[2:18], pattern(300, 2, 2), 17, 283)).to_string());
  return shared(key, ctr);
}
`

// chacha20KernelSweep is the lengths the program prints after the lengths to 1024.
var chacha20KernelSweep = []int{1279, 1280, 1281, 1535, 1536, 1537, 2047, 2048, 2049, 4095, 4096, 4097, 9000}

const (
	rfc8439Block  = "10f1e7e4d13b5915500fdd1fa32071c4c7d1f4c733c068030422aa9ac3d46c4ed2826446079faa0914c2d705d98b02a2b5129cd1de164eb9cbd083e8a2503c4e"
	rfc8439Cipher = "6e2e359a2568f98041ba0728dd0d6981e97e7aec1d4360c20a27afccfd9fae0bf91b65c5524733ab8f593dabcd62b3571639d624e65152ab8f530c359f0861d807ca0dbf500d6a6156a38e088a22b65e52bc514d16ccf806818ce91ab77937365af90bbf74a35be6b40b8eedf2785e42874d"
)

// chacha20Model is RFC 8439's ChaCha20 over data. The key is 32 bytes, and ctr
// is the block counter, little-endian, and then the nonce. The counter wraps.
func chacha20Model(key, ctr, data []byte) []byte {
	in := [16]uint32{0x61707865, 0x3320646e, 0x79622d32, 0x6b206574}
	for k := 0; k < 8; k++ {
		in[4+k] = binary.LittleEndian.Uint32(key[4*k:])
	}
	for k := 0; k < 4; k++ {
		in[12+k] = binary.LittleEndian.Uint32(ctr[4*k:])
	}
	out := append([]byte(nil), data...)
	for off := 0; off < len(out); off += 64 {
		x := in
		qr := func(a, b, c, d int) {
			x[a] += x[b]
			x[d] = bits.RotateLeft32(x[d]^x[a], 16)
			x[c] += x[d]
			x[b] = bits.RotateLeft32(x[b]^x[c], 12)
			x[a] += x[b]
			x[d] = bits.RotateLeft32(x[d]^x[a], 8)
			x[c] += x[d]
			x[b] = bits.RotateLeft32(x[b]^x[c], 7)
		}
		for r := 0; r < 10; r++ {
			qr(0, 4, 8, 12)
			qr(1, 5, 9, 13)
			qr(2, 6, 10, 14)
			qr(3, 7, 11, 15)
			qr(0, 5, 10, 15)
			qr(1, 6, 11, 12)
			qr(2, 7, 8, 13)
			qr(3, 4, 9, 14)
		}
		var ks [64]byte
		for k := range x {
			binary.LittleEndian.PutUint32(ks[4*k:], x[k]+in[k])
		}
		for k := 0; k < 64 && off+k < len(out); k++ {
			out[off+k] ^= ks[k]
		}
		in[12]++
	}
	return out
}

func chacha20Pattern(n, mul, add int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*mul + add) & 255)
	}
	return out
}

func chacha20FNV(b []byte) uint32 {
	h := uint32(2166136261)
	for _, x := range b {
		h = (h ^ uint32(x)) * 16777619
	}
	return h
}

// chacha20Into is the kernel's contract on a buffer: buf[at:at+n] is XORed in place.
func chacha20Into(key, ctr, buf []byte, at, n int) []byte {
	out := append([]byte(nil), buf...)
	copy(out[at:], chacha20Model(key, ctr, buf[at:at+n]))
	return out
}

func chacha20KernelWant(t *testing.T) string {
	t.Helper()
	ctr, err := hex.DecodeString("01000000000000000000004a00000000")
	if err != nil {
		t.Fatal(err)
	}
	sunscreen := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	if got := hex.EncodeToString(chacha20Model(chacha20Pattern(32, 1, 0), ctr, sunscreen)); got != rfc8439Cipher {
		t.Fatalf("the Go model gives %s for RFC 8439 2.4.2", got)
	}
	var b strings.Builder
	b.WriteString("block " + rfc8439Block + "\n")
	b.WriteString("cipher " + rfc8439Cipher + "\n")
	key, base := chacha20Pattern(32, 13, 5), chacha20Pattern(16, 7, 3)
	var lengths []int
	for n := 0; n <= 1024; n++ {
		lengths = append(lengths, n)
	}
	for _, n := range append(lengths, chacha20KernelSweep...) {
		fmt.Fprintf(&b, "%d %d\n", n, chacha20FNV(chacha20Into(key, base, chacha20Pattern(n+9, 3, 1), 5, n)))
	}
	wrap := append([]byte{253, 255, 255, 255}, base[4:]...)
	fmt.Fprintf(&b, "wrap %d\n", chacha20FNV(chacha20Model(key, wrap, chacha20Pattern(700, 5, 7))))
	fmt.Fprintf(&b, "view %d\n", chacha20FNV(chacha20Into(chacha20Pattern(40, 9, 9)[3:35], chacha20Pattern(20, 1, 2)[2:18], chacha20Pattern(300, 2, 2), 17, 283)))
	return b.String()
}

func chacha20KernelCheck(t *testing.T, got []byte, exit int) {
	t.Helper()
	if exit != 0 {
		t.Fatalf("exited %d, want 0: a buffer with another holder or a literal was written in place (see shared)", exit)
	}
	gl, wl := strings.Split(string(got), "\n"), strings.Split(chacha20KernelWant(t), "\n")
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

func chacha20Run(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("did not exit normally\n%s", errb.String())
	}
	chacha20KernelCheck(t, out.Bytes(), cmd.ProcessState.ExitCode())
}

func chacha20Source(t *testing.T, prog string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "chacha20.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostChaCha20KernelX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chacha20Source(t, chacha20KernelProg)
	t.Run("in-process", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		chacha20Run(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("gas", func(t *testing.T) {
		chacha20Run(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
}

func TestSelfHostChaCha20KernelArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chacha20Source(t, chacha20KernelProg)
	t.Run("in-process", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		chacha20Run(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)))
	})
	t.Run("gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		chacha20Run(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))))
	})
}

func TestSelfHostChaCha20KernelWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	chacha20Run(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, chacha20Source(t, chacha20KernelProg), "FERN_STRICT_IR=1")))
}

func TestSelfHostInterpChaCha20Kernel(t *testing.T) {
	if strings.HasPrefix(chacha20KernelProg, "import") {
		t.Fatal("the self-host interpreter driver runs single-file programs only")
	}
	t.Run("go", func(t *testing.T) {
		got, exit := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), chacha20Source(t, chacha20KernelProg))
		chacha20KernelCheck(t, got, exit)
	})
	t.Run("self-host", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		cmd := runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run"))
		cmd.Stdin = strings.NewReader(chacha20KernelProg)
		chacha20Run(t, cmd)
	})
}

// TestSelfHostChaCha20KernelCensus runs the kernel program under the leak
// census on each target. Every allocation is freed, and the copies that the
// kernel makes of shared buffers are among them.
func TestSelfHostChaCha20KernelCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := chacha20Source(t, chacha20KernelProg)
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

func chacha20CensusBalances(t *testing.T, run *exec.Cmd) {
	t.Helper()
	var errb strings.Builder
	run.Stderr = &errb
	_ = run.Run()
	if run.ProcessState == nil || run.ProcessState.ExitCode() != 0 {
		t.Fatalf("the census build did not exit 0\n%s", errb.String())
	}
	summary := leakSummaryLine(errb.String())
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil || summary == "" {
		t.Fatalf("no census line in %q: %v", errb.String(), err)
	}
	if allocs == 0 || allocs != frees || live != 0 || strings.HasSuffix(summary, " from=exit") {
		t.Fatalf("the census does not balance: %s", summary)
	}
}

// chacha20KernelMisuses each stop the program before the kernel reaches
// outside the buffer: a range past its end, a negative offset or length, a key
// that is not 32 bytes, and a counter block that is not 16 bytes.
var chacha20KernelMisuses = []string{
	"__chacha20_xor_into(key, ctr, __alloc_u8(20), 4, 17)",
	"__chacha20_xor_into(key, ctr, __alloc_u8(20), 0 - 1, 4)",
	"__chacha20_xor_into(key, ctr, __alloc_u8(20), 0, 0 - 1)",
	"__chacha20_xor_into(key[0:31], ctr, __alloc_u8(20), 0, 4)",
	"__chacha20_xor_into(__alloc_u8(33), ctr, __alloc_u8(20), 0, 4)",
	"__chacha20_xor_into(key, ctr[0:12], __alloc_u8(20), 0, 4)",
}

func chacha20KernelMisuseProg(call string) string {
	return `function main(): i32 {
  let key: u8[] = __alloc_u8(32);
  let ctr: u8[] = __alloc_u8(16);
  let out: u8[] = ` + call + `;
  print("survived");
  return out.len() - out.len();
}
`
}

func chacha20MisuseStops(t *testing.T, run func(src string) *exec.Cmd) {
	t.Helper()
	for _, call := range chacha20KernelMisuses {
		out, _ := run(chacha20Source(t, chacha20KernelMisuseProg(call))).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostChaCha20KernelMisuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		chacha20MisuseStops(t, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src)) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		chacha20MisuseStops(t, func(src string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)) })
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		wasmtime := e2eharness.Wasmtime(t)
		chacha20MisuseStops(t, func(src string) *exec.Cmd {
			return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
		})
	})
	t.Run("interpreters", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		oracle := buildLangBinForInterp(t)
		for _, call := range chacha20KernelMisuses {
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
