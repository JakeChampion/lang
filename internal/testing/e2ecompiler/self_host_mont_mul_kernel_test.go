package e2ecompiler

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// montMulKernelProg prints the limbs of __mont_mul for moduli of 1 to 40
// limbs. Each modulus is odd. Kind 0 has pseudo-random limbs with the top bit
// set, kind 1 has the top bit clear, kind 2 is all ones with all-ones values,
// and kind 3 multiplies zero. The values of kinds 0 and 1 are pseudo-random
// limbs, so they can be above p. Each line is the count of limbs, the kind
// and the limbs of the result. The last line passes views of a longer array.
const montMulKernelProg = `function next(x: u64): u64 {
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

// -p^-1 mod 2^64 by Newton's iteration.
function n0_of(p0: u64): i64 {
  let x: u64 = 1;
  let i: i32 = 0;
  while (i < 6) {
    x = x * (2u64 - p0 * x);
    i = i + 1;
  }
  return (0u64 - x) as i64;
}

function show(n: i32, kind: i32, t: [i64]): void {
  let out: string = n.to_string() + " " + kind.to_string();
  for w in t {
    out = out + " " + hex64(w as u64);
  }
  print(out);
}

function run(n: i32, kind: i32, seed: u64): void {
  let x: u64 = seed;
  let m: i64[] = __alloc_i64(n + 1);
  let a: i64[] = __alloc_i64(n);
  let b: i64[] = __alloc_i64(n);
  let i: i32 = 0;
  while (i < n) {
    x = next(x);
    let p: u64 = x;
    x = next(x);
    let va: u64 = x;
    x = next(x);
    let vb: u64 = x;
    if (kind == 2) {
      p = 0u64 - 1u64;
      va = p;
      vb = p;
    }
    if (kind == 3) {
      va = 0u64;
    }
    if (i == 0) {
      p = p | 1u64;
    }
    if (i == n - 1 && kind == 0) {
      p = p | 9223372036854775808u64;
    }
    if (i == n - 1 && kind == 1) {
      p = p >> 1u64;
      p = p | 1u64;
    }
    m = m.with(i + 1, p as i64);
    a = a.with(i, va as i64);
    b = b.with(i, vb as i64);
    i = i + 1;
  }
  m = m.with(0, n0_of(m[1] as u64));
  show(n, kind, __mont_mul(a, b, m));
}

function main(): i32 {
  let n: i32 = 1;
  while (n <= 40) {
    let kind: i32 = 0;
    while (kind < 4) {
      run(n, kind, (n * 4 + kind) as u64);
      kind = kind + 1;
    }
    n = n + 1;
  }
  let big: i64[] = [7, 9, 11, 13, 5, 0 - 1, 1, 2, 3];
  let m: i64[] = [0, 0, 0 - 3];
  m = m.with(1, 13);
  m = m.with(0, n0_of(13u64));
  show(2, 9, __mont_mul(big[1:3], big[5:7], m[0:3]));
  return 0;
}
`

// montMulCase gives the inputs of one line of montMulKernelProg.
func montMulCase(n, kind int, seed uint64) (a, b, m []uint64) {
	next := func(x uint64) uint64 { return x*6364136223846793005 + 1442695040888963407 }
	x := seed
	m = make([]uint64, n+1)
	a = make([]uint64, n)
	b = make([]uint64, n)
	for i := range n {
		x = next(x)
		p := x
		x = next(x)
		va := x
		x = next(x)
		vb := x
		if kind == 2 {
			p, va, vb = ^uint64(0), ^uint64(0), ^uint64(0)
		}
		if kind == 3 {
			va = 0
		}
		if i == 0 {
			p |= 1
		}
		if i == n-1 && kind == 0 {
			p |= 1 << 63
		}
		if i == n-1 && kind == 1 {
			p = p>>1 | 1
		}
		m[i+1], a[i], b[i] = p, va, vb
	}
	m[0] = montN0(m[1])
	return a, b, m
}

func montN0(p0 uint64) uint64 {
	x := uint64(1)
	for range 6 {
		x *= 2 - p0*x
	}
	return -x
}

func montBig(limbs []uint64) *big.Int {
	v := new(big.Int)
	for i := len(limbs) - 1; i >= 0; i-- {
		v.Lsh(v, 64)
		v.Or(v, new(big.Int).SetUint64(limbs[i]))
	}
	return v
}

// montMulChecked is interp.MontMul, after a check against math/big: the
// result is a b R^-1 mod p, and it is below R.
func montMulChecked(t *testing.T, a, b, m []uint64) []uint64 {
	t.Helper()
	got := interp.MontMul(a, b, m)
	p := montBig(m[1:])
	r := new(big.Int).Lsh(big.NewInt(1), uint(64*len(a)))
	want := new(big.Int).Mul(montBig(a), montBig(b))
	want.Mul(want, new(big.Int).ModInverse(r, p))
	want.Mod(want, p)
	if g := montBig(got); new(big.Int).Mod(g, p).Cmp(want) != 0 || g.Cmp(r) >= 0 {
		t.Fatalf("the CIOS model is not a b R^-1 mod p for a=%x b=%x p=%x", a, b, m[1:])
	}
	return got
}

func montMulLine(n, kind int, t []uint64) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d %d", n, kind)
	for _, w := range t {
		fmt.Fprintf(&sb, " %016x", w)
	}
	return sb.String()
}

func montMulKernelWant(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for n := 1; n <= 40; n++ {
		for kind := range 4 {
			x, y, m := montMulCase(n, kind, uint64(n*4+kind))
			fmt.Fprintln(&b, montMulLine(n, kind, montMulChecked(t, x, y, m)))
		}
	}
	m := []uint64{montN0(13), 13, ^uint64(2)}
	fmt.Fprintln(&b, montMulLine(2, 9, montMulChecked(t, []uint64{9, 11}, []uint64{^uint64(0), 1}, m)))
	return b.String()
}

func montMulRun(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("did not exit with 0\n%s", errb.String())
	}
	gl, wl := strings.Split(out.String(), "\n"), strings.Split(montMulKernelWant(t), "\n")
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

func montMulSource(t *testing.T, prog string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "montmul.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSelfHostMontMulKernelX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := montMulSource(t, montMulKernelProg)
	t.Run("in-process", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "prog")
		if out, err := cli.compileCmd("x86-64-linux", bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		montMulRun(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("gas", func(t *testing.T) {
		montMulRun(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)))
	})
}

func TestSelfHostMontMulKernelArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := montMulSource(t, montMulKernelProg)
	t.Run("in-process", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		montMulRun(t, e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)))
	})
	t.Run("gas", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		montMulRun(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "prog", string(asm))))
	})
}

func TestSelfHostMontMulKernelWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	montMulRun(t, exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, montMulSource(t, montMulKernelProg), "FERN_STRICT_IR=1")))
}

func TestSelfHostInterpMontMulKernel(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		got, exit := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), montMulSource(t, montMulKernelProg))
		if exit != 0 {
			t.Fatalf("exited %d\n%s", exit, got)
		}
		if want := montMulKernelWant(t); string(got) != want {
			t.Fatalf("Go interpreter output differs from the model:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("self-host", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		cmd := runX86_64Bin(runner, buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run"))
		cmd.Stdin = strings.NewReader(montMulKernelProg)
		montMulRun(t, cmd)
	})
}

// TestSelfHostMontMulKernelCensus runs the kernel program under the leak
// census on each target. The arrays that the kernel makes are freed.
func TestSelfHostMontMulKernelCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := montMulSource(t, montMulKernelProg)
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

// montMulKernelMisuses each stop the program before the kernel reads past a
// value: a modulus with no limbs, and values with more or fewer limbs than
// the modulus.
var montMulKernelMisuses = []string{
	"__mont_mul(v[0:2], v[0:2], v[0:1])",
	"__mont_mul(v[0:3], v[0:2], v[0:3])",
	"__mont_mul(v[0:2], v[0:1], v[0:3])",
	"__mont_mul(v[0:1], v[0:1], v[0:3])",
}

func montMulKernelMisuseProg(call string) string {
	return `function main(): i32 {
  let v: i64[] = [1, 3, 5, 7];
  let out: i64[] = ` + call + `;
  print("survived");
  return out.len() - out.len();
}
`
}

func montMulMisuseStops(t *testing.T, run func(src string) *exec.Cmd) {
	t.Helper()
	for _, call := range montMulKernelMisuses {
		out, _ := run(montMulSource(t, montMulKernelMisuseProg(call))).CombinedOutput()
		if strings.Contains(string(out), "survived") {
			t.Errorf("%s returned instead of stopping the program:\n%s", call, out)
		}
	}
}

func TestSelfHostMontMulKernelMisuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("x86-64-linux", func(t *testing.T) {
		montMulMisuseStops(t, func(src string) *exec.Cmd { return runX86_64Bin(cli.runner, cli.x86Binary(t, src)) })
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		montMulMisuseStops(t, func(src string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src)) })
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		wasmtime := e2eharness.Wasmtime(t)
		montMulMisuseStops(t, func(src string) *exec.Cmd {
			return exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
		})
	})
	t.Run("interpreters", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		gcc, runner := x86_64Tooling(t)
		driver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
		oracle := buildLangBinForInterp(t)
		for _, call := range montMulKernelMisuses {
			prog := montMulKernelMisuseProg(call)
			if got := interpExitStdin(t, oracle, prog, ""); got == 0 {
				t.Errorf("Go interpreter: %s returned instead of stopping the program", call)
			}
			if got := runDriverExit(t, runner, driver, []byte(prog)); got == 0 {
				t.Errorf("self-host interpreter: %s returned instead of stopping the program", call)
			}
		}
	})
}
