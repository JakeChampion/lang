package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// f64LogBitsFern prints the bits of std/f64's log for each integer from 1 to
// 2^20 and for 2^20 random doubles, then the bits of fma for 2^17 random
// triples. f64LogBitsC prints the same values from the C library. Both make
// the random values with the same xorshift generator.
const f64LogBitsFern = `import "std/f64";

function next(s: i64): i64 {
  s = s ^ s << 13;
  s = s ^ (s >> 7 & 0x01ffffffffffffff);
  return s ^ s << 17;
}

function rand_log_input(u: i64, class: i64): f64 {
  let low = u >> 1 & 0x7fffffffffffffff;
  if (class == 0) {
    return f64_from_bits(low);
  }
  if (class == 1) {
    return f64_from_bits(0x3fe0000000000000 | (u >> 12 & 0x000fffffffffffff)) * 2.0;
  }
  if (class == 2) {
    return f64_from_bits(0x3fee000000000000 + low % 0x0003090000000000);
  }
  return f64_from_bits(u);
}

function rand_fma_input(u: i64): f64 {
  return f64_from_bits(0x3c00000000000000 + (u >> 1 & 0x7fffffffffffffff) % 0x0080000000000000 | (u & 1) << 63);
}

function main(): i32 {
  let chunk = "";
  let i: i64 = 1;
  while (i <= 1048576) {
    chunk = chunk + f64_bits((i as f64).log()).to_string() + "\n";
    if (i % 65536 == 0) {
      print(chunk);
      chunk = "";
    }
    i += 1;
  }
  let s: i64 = -7046029254386353131;
  let j: i64 = 0;
  while (j < 1048576) {
    s = next(s);
    chunk = chunk + f64_bits(rand_log_input(s, j & 3).log()).to_string() + "\n";
    j += 1;
    if (j % 65536 == 0) {
      print(chunk);
      chunk = "";
    }
  }
  let k: i64 = 0;
  while (k < 131072) {
    s = next(s);
    let a = rand_fma_input(s);
    s = next(s);
    let b = rand_fma_input(s);
    s = next(s);
    let c = if ((k & 1) == 0) { rand_fma_input(s) } else { -(a * b) };
    chunk = chunk + f64_bits(a.fma(b, c)).to_string() + "\n";
    k += 1;
  }
  print(chunk);
  return 0;
}
`

const f64LogBitsC = `#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#ifdef __GLIBC__
#include <gnu/libc-version.h>
#endif

static uint64_t next(uint64_t s) {
  s ^= s << 13;
  s ^= s >> 7;
  return s ^ (s << 17);
}

static double from_bits(uint64_t u) { double x; memcpy(&x, &u, 8); return x; }

static void put(double y) {
  int64_t b;
  memcpy(&b, &y, 8);
  printf("%lld\n", (long long)b);
}

static double rand_log_input(uint64_t u, int class) {
  uint64_t low = u >> 1;
  if (class == 0) return from_bits(low);
  if (class == 1) return from_bits(0x3fe0000000000000ULL | (u >> 12)) * 2.0;
  if (class == 2) return from_bits(0x3fee000000000000ULL + low % 0x0003090000000000ULL);
  return from_bits(u);
}

static double rand_fma_input(uint64_t u) {
  return from_bits((0x3c00000000000000ULL + (u >> 1) % 0x0080000000000000ULL) | (u & 1) << 63);
}

int main(void) {
#ifdef __GLIBC__
  int major, minor;
  if (sscanf(gnu_get_libc_version(), "%d.%d", &major, &minor) != 2 || major < 2 || (major == 2 && minor < 28)) return 3;
#else
  return 3;
#endif
  for (long i = 1; i <= 1048576; i++) put(log((double)i));
  uint64_t s = 0x9e3779b97f4a7c15ULL;
  for (long j = 0; j < 1048576; j++) {
    s = next(s);
    put(log(rand_log_input(s, (int)(j & 3))));
  }
  for (long k = 0; k < 131072; k++) {
    s = next(s);
    double a = rand_fma_input(s);
    s = next(s);
    double b = rand_fma_input(s);
    s = next(s);
    volatile double p = a * b;
    double c = (k & 1) == 0 ? rand_fma_input(s) : -p;
    put(fma(a, b, c));
  }
  return 0;
}
`

// TestSelfHostF64LogMatchesGlibc checks that std/f64's log gives the bits of
// the log of glibc (2.28 and later) at each integer from 1 to 2^20 and at 2^20
// random doubles, and that fma rounds as the C library does. It runs the
// program on x86-64, arm64 and wasm32-wasi. The oracle is the C library of
// the host. Two NaN results match, whatever their bits.
func TestSelfHostF64LogMatchesGlibc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the oracle is the log of glibc")
	}
	dir := t.TempDir()
	csrc := filepath.Join(dir, "oracle.c")
	if err := os.WriteFile(csrc, []byte(f64LogBitsC), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := filepath.Join(dir, "oracle")
	if msg, err := exec.Command("cc", "-O1", "-o", oracle, csrc, "-lm").CombinedOutput(); err != nil {
		t.Fatalf("cc: %v\n%s", err, msg)
	}
	cmd := exec.Command(oracle)
	want, err := cmd.Output()
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 3 {
		t.Skip("the C library of the host is not glibc 2.28 or later")
	}
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	src := filepath.Join(dir, "log_bits.fern")
	if err := os.WriteFile(src, []byte(f64LogBitsFern), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var run *exec.Cmd
			switch target {
			case "x86-64-linux":
				run = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				run = runArm64Bin(qemu, cli.arm64Binary(t, src))
			default:
				run = exec.Command(e2eharness.Wasmtime(t), "run", cli.emit(t, src, target))
			}
			var stderr bytes.Buffer
			run.Stderr = &stderr
			got, err := run.Output()
			if err != nil {
				t.Fatalf("%s: %v\n%s", target, err, stderr.String())
			}
			compareF64Bits(t, string(got), string(want))
		})
	}
}

// compareF64Bits compares two lists of f64 bits, one value on each line.
// Empty lines do not count.
func compareF64Bits(t *testing.T, got, want string) {
	t.Helper()
	g := strings.Fields(got)
	w := strings.Fields(want)
	if len(g) != len(w) {
		t.Fatalf("got %d values, want %d", len(g), len(w))
	}
	bad := 0
	for i := range w {
		if g[i] == w[i] || (isNaNBits(g[i]) && isNaNBits(w[i])) {
			continue
		}
		if bad < 10 {
			t.Errorf("value %d: got bits %s, want %s", i, g[i], w[i])
		}
		bad++
	}
	if bad > 0 {
		t.Errorf("%d of %d values differ", bad, len(w))
	}
}

// isNaNBits tells whether the decimal bits of an f64 are those of a NaN.
func isNaNBits(s string) bool {
	var b uint64
	neg := strings.HasPrefix(s, "-")
	for _, c := range strings.TrimPrefix(s, "-") {
		b = b*10 + uint64(c-'0')
	}
	if neg {
		b = -b
	}
	return b&0x7ff0000000000000 == 0x7ff0000000000000 && b&0x000fffffffffffff != 0
}
