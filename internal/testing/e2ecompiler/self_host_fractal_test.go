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
function new_escape_state(limit: i32): EscapeState {
  return EscapeState { limit: limit, magnitude_squared: -1.0, steps: 0 };
}

function main(): i32 {
  let result: EscapeState = new_escape_state(64);
  let raw: u8[] = __alloc_u8(3);
  let mark: i64 = __heap_alloc_count();
  let index: i32 = 0;
  while (index < 128) {
    let real: f64 = 0.0;
    let imaginary: f64 = 0.0;
    if (index % 4 == 1) { real = -1.0; }
    if (index % 4 == 2) { real = 2.0; imaginary = 2.0; }
    if (index % 4 == 3) { imaginary = 1.0; }
    result = escape(result, real, imaginary);
    if (index % 4 < 2 && (result.steps != 0 || result.magnitude_squared != -1.0)) { return 1; }
    if (index % 4 == 2 && (result.steps != 4 || result.magnitude_squared != 112356712.0)) { return 2; }
    if (index % 4 == 3 && (result.steps != 64 || result.magnitude_squared != -1.0)) { return 3; }
    raw = write_pixel(raw, 0, index as u8, 255 as u8, 0 as u8);
    index = index + 1;
  }
  if (__heap_alloc_count() != mark) { return 4; }
  if (raw[0] != 127 as u8 || raw[1] != 255 as u8 || raw[2] != 0 as u8) { return 5; }

  let saved_result: EscapeState = result;
  result = escape(result, 2.0, 2.0);
  if (saved_result.steps != 64 || saved_result.magnitude_squared != -1.0) { return 6; }
  if (result.steps != 4 || result.magnitude_squared != 112356712.0) { return 7; }
  let saved_raw: u8[] = raw;
  raw = write_pixel(raw, 0, 1 as u8, 2 as u8, 3 as u8);
  if (saved_raw[0] != 127 as u8 || saved_raw[1] != 255 as u8 || saved_raw[2] != 0 as u8) { return 8; }
  if (raw[0] != 1 as u8 || raw[1] != 2 as u8 || raw[2] != 3 as u8) { return 9; }
  return 0;
}
`
