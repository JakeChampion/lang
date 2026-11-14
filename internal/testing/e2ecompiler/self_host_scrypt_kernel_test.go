package e2ecompiler

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// scryptKernelProg prints the output blocks of __scrypt_block_mix for r = 1
// to 5 and r = 8. b holds four blocks of 16r pseudo-random slots: the input,
// the block that is mixed in, and two outputs. The second call mixes the
// first output with the input into the last block, so mix comes before from.
// The third call mixes the input with itself, which gives a zero input. A
// copy of b is kept across the first call, so the kernel must copy b, and
// each line ends with a slot of that copy.
const scryptKernelProg = `function next(x: u64): u64 {
  return x * 6364136223846793005u64 + 1442695040888963407u64;
}

function hex64(v: u64): string {
  let d: string[] = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "a", "b", "c", "d", "e", "f"];
  let out: string = "";
  let i: i32 = 15;
  while (i >= 0) {
    out = out + d[(v >> (4 * i) as u64 & 15u64) as i32];
    i = i - 1;
  }
  return out;
}

function show(r: i32, call: i32, b: [i64], at: i32, kept: i64): void {
  let out: string = r.to_string() + " " + call.to_string();
  let i: i32 = 0;
  while (i < 16 * r) {
    out = out + " " + hex64(b[at + i] as u64);
    i = i + 1;
  }
  print(out + " " + hex64(kept as u64));
}

function run(r: i32): void {
  let h: i32 = 16 * r;
  let x: u64 = r as u64;
  let b: i64[] = __alloc_i64(4 * h);
  let i: i32 = 0;
  while (i < 4 * h) {
    x = next(x);
    b = b.with(i, x as i64);
    i = i + 1;
  }
  let kept: i64[] = b;
  b = __scrypt_block_mix(b, 0, h, 2 * h, r);
  show(r, 0, b, 2 * h, kept[2 * h]);
  b = __scrypt_block_mix(b, 2 * h, 0, 3 * h, r);
  show(r, 1, b, 3 * h, kept[3 * h]);
  b = __scrypt_block_mix(b, h, h, 3 * h, r);
  show(r, 2, b, 3 * h, kept[3 * h]);
}

function main(): i32 {
  let r: i32 = 1;
  while (r <= 5) {
    run(r);
    r = r + 1;
  }
  run(8);
  return 0;
}
`

// scryptOrder writes the 16 words of a block in RFC order to the slots of
// the kernel's order.
func scryptOrder(w [16]uint32) [8]uint64 {
	var out [8]uint64
	for k := range 8 {
		out[k] = uint64(w[interp.ScryptOrder[2*k]]) | uint64(w[interp.ScryptOrder[2*k+1]])<<32
	}
	return out
}

func scryptKernelWant(t *testing.T) string {
	t.Helper()
	// The Salsa20/8 core test vector of RFC 7914, section 8.
	in, _ := hex.DecodeString("7e879a214f3ec9867ca940e641718f26baee555b8c61c1b50df846116dcd3b1dee24f319df9b3d8514121e4b5ac5aa3276021d2909c74829edebc68db8b8c25e")
	want, _ := hex.DecodeString("a41f859c6608cc993b81cacb020cef05044b2181a2fd337dfd7b1c6396682f29b4393168e3c9e6bcfe6bc5b7a06d96bae424cc102c91745c24ad673dc7618f81")
	var w [16]uint32
	for k := range w {
		w[k] = binary.LittleEndian.Uint32(in[4*k:])
	}
	got := interp.Salsa208(w)
	for k := range got {
		if got[k] != binary.LittleEndian.Uint32(want[4*k:]) {
			t.Fatalf("the Salsa20/8 model does not give the RFC 7914 output at word %d", k)
		}
	}
	// With r = 1, a zero second half and a zero block mixed in, BlockMix is
	// two cores in a chain: Y0 is the core of the first half, Y1 the core of Y0.
	b := make([]uint64, 48)
	var x0 [16]uint32
	for k := range 8 {
		b[k] = uint64(k+1) | uint64(k+9)<<32
		x0[interp.ScryptOrder[2*k]], x0[interp.ScryptOrder[2*k+1]] = uint32(k+1), uint32(k+9)
	}
	interp.ScryptBlockMix(b, 0, 32, 16, 1)
	y0 := interp.Salsa208(x0)
	if scryptOrder(y0) != [8]uint64(b[16:24]) || scryptOrder(interp.Salsa208(y0)) != [8]uint64(b[24:32]) {
		t.Fatalf("the BlockMix model does not chain two cores")
	}
	var sb strings.Builder
	for _, r := range []int{1, 2, 3, 4, 5, 8} {
		h := 16 * r
		b := make([]uint64, 4*h)
		x := uint64(r)
		for k := range b {
			x = x*6364136223846793005 + 1442695040888963407
			b[k] = x
		}
		kept := append([]uint64(nil), b...)
		for call, args := range [][3]int{{0, h, 2 * h}, {2 * h, 0, 3 * h}, {h, h, 3 * h}} {
			interp.ScryptBlockMix(b, args[0], args[1], args[2], r)
			fmt.Fprintf(&sb, "%d %d", r, call)
			for _, v := range b[args[2] : args[2]+h] {
				fmt.Fprintf(&sb, " %016x", v)
			}
			fmt.Fprintf(&sb, " %016x\n", kept[args[2]])
		}
	}
	return sb.String()
}

func scryptKernelRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	kernelOutputMatches(t, cmd, scryptKernelWant(t))
}

// kernelOutputMatches runs cmd and compares its output with want, line by line.
func kernelOutputMatches(t *testing.T, cmd *exec.Cmd, want string) {
	t.Helper()
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("did not exit with 0\n%s", errb.String())
	}
	gl, wl := strings.Split(out.String(), "\n"), strings.Split(want, "\n")
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

func scryptKernelSource(t *testing.T, prog string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "scrypt.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostScryptKernelX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := scryptKernelSource(t, scryptKernelProg)
	t.Run("in-process", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		scryptKernelRun(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("gas", func(t *testing.T) {
		scryptKernelRun(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
}

func TestSelfHostScryptKernelArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := scryptKernelSource(t, scryptKernelProg)
	t.Run("in-process", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		scryptKernelRun(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)))
	})
	t.Run("gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		scryptKernelRun(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))))
	})
}

func TestSelfHostScryptKernelWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	scryptKernelRun(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, scryptKernelSource(t, scryptKernelProg), "FERN_STRICT_IR=1")))
}

func TestSelfHostInterpScryptKernel(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		got, exit := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), scryptKernelSource(t, scryptKernelProg))
		if exit != 0 {
			t.Fatalf("exited %d\n%s", exit, got)
		}
		if want := scryptKernelWant(t); string(got) != want {
			t.Fatalf("Go interpreter output differs from the model:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("self-host", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		cmd := runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run"))
		cmd.Stdin = strings.NewReader(scryptKernelProg)
		scryptKernelRun(t, cmd)
	})
}

// TestSelfHostScryptKernelCensus runs the kernel program under the leak
// census on each target. The copy that the kernel makes is freed.
func TestSelfHostScryptKernelCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := scryptKernelSource(t, scryptKernelProg)
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

// scryptKernelMisuses each stop the program before the kernel reads or
// writes past b, or writes over an input: r below 1, a negative offset, a
// block past the end, and an output that overlaps each input.
var scryptKernelMisuses = []string{
	"__scrypt_block_mix(b, 0, 16, 32, 0)",
	"__scrypt_block_mix(b, 0 - 16, 16, 32, 1)",
	"__scrypt_block_mix(b, 0, 16, 40, 1)",
	"__scrypt_block_mix(b, 0, 16, 8, 1)",
	"__scrypt_block_mix(b, 0, 16, 24, 1)",
	"__scrypt_block_mix(b, 0, 16, 32, 2)",
}

func scryptKernelMisuseProg(call string) string {
	return `function main(): i32 {
  let b: i64[] = __alloc_i64(48);
  b = ` + call + `;
  print("survived");
  return b.len() - b.len();
}
`
}

// scryptMisuseStops also runs a valid call, which must print, so a program
// that does not compile cannot pass for one that stops.
func scryptMisuseStops(t *testing.T, run func(src string) *exec.Cmd) {
	t.Helper()
	if out, _ := run(scryptKernelSource(t, scryptKernelMisuseProg("__scrypt_block_mix(b, 0, 16, 32, 1)"))).CombinedOutput(); !strings.Contains(string(out), "survived") {
		t.Fatalf("a valid call did not run to the end:\n%s", out)
	}
	for _, call := range scryptKernelMisuses {
		out, _ := run(scryptKernelSource(t, scryptKernelMisuseProg(call))).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostScryptKernelMisuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		scryptMisuseStops(t, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src)) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		scryptMisuseStops(t, func(src string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)) })
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		wasmtime := e2eharness.Wasmtime(t)
		scryptMisuseStops(t, func(src string) *exec.Cmd {
			return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
		})
	})
	t.Run("interpreters", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		oracle := buildLangBinForInterp(t)
		valid := scryptKernelMisuseProg("__scrypt_block_mix(b, 0, 16, 32, 1)")
		if interpExitStdin(t, oracle, valid, "") != 0 || runDriverExit(t, runner, driver, []byte(valid)) != 0 {
			t.Fatalf("a valid call did not run to the end in an interpreter")
		}
		for _, call := range scryptKernelMisuses {
			prog := scryptKernelMisuseProg(call)
			if got := interpExitStdin(t, oracle, prog, ""); got == 0 {
				t.Errorf("Go interpreter: %s returned instead of stopping the program", call)
			}
			if got := runDriverExit(t, runner, driver, []byte(prog)); got == 0 {
				t.Errorf("self-host interpreter: %s returned instead of stopping the program", call)
			}
		}
	})
}
