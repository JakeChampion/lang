package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostFractalTiles(t *testing.T) {
	path, err := filepath.Abs("../../../examples/fractal/fractal_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path)
}

func TestSelfHostFractalSource(t *testing.T) {
	path, err := filepath.Abs("../../../examples/fractal/source_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path)
}

func TestSelfHostFractalFipKernels(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"fractal.fern", "binary.fern"} {
		source, err := os.ReadFile(filepath.Join("../../../examples/fractal", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "fractal.fern" {
			// Test the private kernels in their module without public test APIs.
			source = append(source, []byte(fractalKernelMain)...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), source, 0600); err != nil {
			t.Fatal(err)
		}
	}
	testFractalTargets(t, filepath.Join(dir, "fractal.fern"))
}

func testFractalTargets(t *testing.T, path string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

const fractalKernelMain = `
// Keep initialization dynamic so the compiler does not use a shared constant record.
@noinline
function new_escape_state(limit: i32): EscapeBatch {
  return EscapeBatch {
    limit: limit, magnitude0: -1.0, magnitude1: -1.0, magnitude2: -1.0, magnitude3: -1.0,
    steps0: 0, steps1: 0, steps2: 0, steps3: 0,
  };
}

function main(): i32 {
  let result: EscapeBatch = new_escape_state(64);
  let raw: u8[] = __alloc_u8(3);
  let colors: u8[] = __alloc_u8(3);
  let mark: i64 = __heap_alloc_count();
  let index: i32 = 0;
  while (index < 128) {
    result = escape_batch(result, 0.0, -1.0, 2.0, -1.7548776662466927, 0.0);
    if (result.steps0 != 0 || result.magnitude0 != -1.0 || result.steps1 != 0 || result.magnitude1 != -1.0) { return 1; }
    if (result.steps2 != 4 || result.magnitude2 != 2090916.0) { return 2; }
    if (result.steps3 != 64 || result.magnitude3 != -1.0) { return 3; }
    let position: f64 = index as f64 / 128.0;
    colors = write_pixel(colors, 0, channel(position, 0), channel(position, 1), channel(position, 2));
    raw = write_pixel(raw, 0, index as u8, 255 as u8, 0 as u8);
    index = index + 1;
  }
  if (__heap_alloc_count() != mark) { return 4; }
  if (raw[0] != 127 as u8 || raw[1] != 255 as u8 || raw[2] != 0 as u8) { return 5; }
  if (colors[0] != 0 as u8 || colors[1] != 6 as u8 || colors[2] != 94 as u8) { return 13; }

  let saved_result: EscapeBatch = result;
  result = escape_batch(result, 2.0, 0.0, -1.0, 2.0, 2.0);
  if (saved_result.steps0 != 0 || saved_result.magnitude0 != -1.0 || saved_result.steps3 != 64) { return 6; }
  if (result.steps0 != 4 || result.magnitude0 != 112356712.0) { return 7; }
  let saved_raw: u8[] = raw;
  raw = write_pixel(raw, 0, 1 as u8, 2 as u8, 3 as u8);
  if (saved_raw[0] != 127 as u8 || saved_raw[1] != 255 as u8 || saved_raw[2] != 0 as u8) { return 8; }
  if (raw[0] != 1 as u8 || raw[1] != 2 as u8 || raw[2] != 3 as u8) { return 9; }
  result = new_escape_state(512);
  result = escape_batch(result, 0.0, -1.7548776662466927, 2.0, 0.3, 0.0);
  if (result.magnitude1 != -1.0 || result.steps1 >= 512 || result.steps1 <= 0) { return 10; }
  result = escape_batch(result, 0.0, 2.0, -1.0, 2.0, 1.0);
  if (result.magnitude0 != -1.0 || result.steps0 != 32) { return 11; }
  if (!differential()) { return 12; }
  return 0;
}

struct ScalarEscape { magnitude: f64, steps: i32 }

// The scalar oracle performs every iteration without analytic or cycle shortcuts.
function scalar_escape(real: f64, imaginary: f64, limit: i32): ScalarEscape {
  let zr: f64 = 0.0;
  let zi: f64 = 0.0;
  let steps: i32 = 0;
  while (steps < limit) {
    let zr2: f64 = zr * zr;
    let zi2: f64 = zi * zi;
    if (zr2 + zi2 > 65536.0) {
      return ScalarEscape { magnitude: zr2 + zi2, steps: steps };
    }
    zi = 2.0 * zr * zi + imaginary;
    zr = zr2 - zi2 + real;
    steps = steps + 1;
  }
  return ScalarEscape { magnitude: -1.0, steps: limit };
}

function agrees(magnitude: f64, steps: i32, real: f64, imaginary: f64, limit: i32): boolean {
  let scalar: ScalarEscape = scalar_escape(real, imaginary, limit);
  if (scalar.magnitude < 0.0) {
    return magnitude < 0.0 && steps >= 0 && steps <= limit;
  }
  return magnitude == scalar.magnitude && steps == scalar.steps;
}

function differential(): boolean {
  let limits: i32[] = [0, 1, 4, 5, 15, 16, 17, 63, 64, 65, 256, 1520, 2944];
  for limit in limits {
    let result: EscapeBatch = new_escape_state(limit);
    let index: i32 = 0;
    while (index < 96) {
      let imaginary: f64 = (index * 37 % 257) as f64 / 64.0 - 2.0;
      let real0: f64 = (index * 53 % 257) as f64 / 64.0 - 2.5;
      let real1: f64 = (index * 89 % 257) as f64 / 64.0 - 2.5;
      let real2: f64 = (index * 131 % 257) as f64 / 64.0 - 2.5;
      let real3: f64 = (index * 181 % 257) as f64 / 64.0 - 2.5;
      if (index < 16) {
        imaginary = 0.13182590420531198;
        real0 = -0.7436438870371587 + index as f64 * 0.00000000000001;
        real1 = real0 - 0.0000000000001;
        real2 = -1.7548776662466927;
        real3 = 0.0;
      }
      result = escape_batch(result, real0, real1, real2, real3, imaginary);
      if (!agrees(result.magnitude0, result.steps0, real0, imaginary, limit)
        || !agrees(result.magnitude1, result.steps1, real1, imaginary, limit)
        || !agrees(result.magnitude2, result.steps2, real2, imaginary, limit)
        || !agrees(result.magnitude3, result.steps3, real3, imaginary, limit)) { return false; }
      index = index + 1;
    }
  }
  return true;
}
`
