package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Every cell read is a snapshot: replacing the shared slot must not alter an
// older read or its input, and repeated replacements must reclaim both arrays.
func scalarArrayCellSources() []struct{ name, source string } {
	var sources []struct{ name, source string }
	for _, tc := range []struct{ name, typ, value, observe, want string }{
		{"byte", "u8", "255 as u8", "x as i32", "255"},
		{"signed32", "i32", "-2147483647", "x", "-2147483647"},
		{"unsigned32", "u32", "4294967295u32", "x / 2u32", "2147483647u32"},
		{"signed64", "i64", "-9223372036854775807i64", "x", "-9223372036854775807i64"},
		{"unsigned64", "u64", "18446744073709551615u64", "x / 2u64", "9223372036854775807u64"},
		{"size", "usize", "65537 as usize", "x", "65537 as usize"},
		{"boolean", "boolean", "true", "x", "true"},
		{"boolean-false", "boolean", "false", "x", "false"},
		{"float32", "f32", "f32_from_bits(2143289345)", "f32_bits(x)", "2143289345"},
		{"float64", "f64", "f64_from_bits(9221120237041090561i64)", "f64_bits(x)", "9221120237041090561i64"},
		{"float-alias", "float", "f64_from_bits(9221120237041090561i64)", "f64_bits(x)", "9221120237041090561i64"},
		{"float32-zero", "f32", "f32_from_bits(-2147483647 - 1)", "f32_bits(x)", "-2147483647 - 1"},
		{"float32-infinity", "f32", "f32_from_bits(2139095040)", "f32_bits(x)", "2139095040"},
		{"float32-subnormal", "f32", "f32_from_bits(1)", "f32_bits(x)", "1"},
		{"float64-zero", "f64", "f64_from_bits(-9223372036854775807i64 - 1i64)", "f64_bits(x)", "-9223372036854775807i64 - 1i64"},
		{"float64-infinity", "f64", "f64_from_bits(9218868437227405312i64)", "f64_bits(x)", "9218868437227405312i64"},
		{"float64-subnormal", "f64", "f64_from_bits(1i64)", "f64_bits(x)", "1i64"},
	} {
		source := strings.NewReplacer("TYPE", tc.typ, "VALUE", tc.value, "OBSERVE", tc.observe, "WANT", tc.want).Replace(`
struct Shared { slot: Cell[TYPE[]] }
function check(x: TYPE): void { assert((OBSERVE) == (WANT)); }
function exercise(): void {
  let input: TYPE[] = [VALUE];
  let empty: TYPE[] = [];
  let inferred = cell_new(empty);
  assert(inferred.get().len() == 0);
  inferred.set(input);
  check(inferred.get()[0]);
  let first: Cell[TYPE[]] = cell_new(empty);
  assert(first.get().len() == 0);
  first.set(input);
  check(first.get()[0]);
  let c: Cell[TYPE[]] = cell_new(input);
  let old: TYPE[] = c.get();
  let alias: Shared = Shared { slot: c };
  let replace: () => void = (): void => { alias.slot.set([]); };
  replace();
  assert(c.get().len() == 0);
  check(input[0]);
  check(old[0]);
  let i: i32 = 0;
  while (i < 32) {
    c.set([VALUE]);
    c.set(c.get());
    let snapshot: TYPE[] = c.get();
    c.set(c.get().append(VALUE));
    assert(snapshot.len() == 1 && c.get().len() == 2);
    check(snapshot[0]);
    check(c.get()[1]);
    i = i + 1;
  }
  c.set([]);
  check(old[0]);
}

function main(): i32 {
  for i in 0..8 { exercise(); }
  return 0;
}
`)
		sources = append(sources, struct{ name, source string }{tc.name, source})
	}
	return sources
}

func TestSelfHostScalarArrayCells(t *testing.T) {
	cli := buildSelfHostCLI(t)
	reference := buildLangBinForInterp(t)
	for _, tc := range scalarArrayCellSources() {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scalar-cell.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
			t.Run("interpreter", func(t *testing.T) {
				cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("interpreter: %v\n%s", err, out)
				}
			})
			t.Run("reference-interpreter", func(t *testing.T) {
				if out, err := exec.Command(reference, "-interp", path).CombinedOutput(); err != nil {
					t.Fatalf("reference interpreter: %v\n%s", err, out)
				}
			})
		})
	}
}

const scalarArrayCellModule = `pub function save(): i32 {
  let words: u64[] = [18446744073709551615u64];
  let c: Cell[u64[]] = cell_new(words);
  let saved: u64[] = c.get();
  c.set([]);
  assert(saved[0] / 2u64 == 9223372036854775807u64);
  assert(words[0] == saved[0]);
  let floats: f64[] = [f64_from_bits(9221120237041090561i64)];
  let f: Cell[f64[]] = cell_new(floats);
  let prior: f64[] = f.get();
  f.set([]);
  assert(f64_bits(prior[0]) == 9221120237041090561i64);
  return c.get().len() + f.get().len();
}
`

func TestSelfHostPerModuleScalarArrayCells(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, scalarArrayCellModule, nil)
}

func TestSelfHostInterpreterScalarArrayCells(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	bin := filepath.Join(dir, "interp")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", bin, filepath.Join(dir, "drivers/interp_run.fern"), cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile interpreter: %v\n%s", err, out)
	}
	cmd = runX86_64Bin(cli.runner, bin)
	cmd.Stdin = bytes.NewBufferString(scalarArrayCellModule + "function main(): i32 { return save(); }\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-built interpreter: %v\n%s", err, out)
	}
}

func TestSelfHostArm64DarwinScalarArrayCells(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, tc := range scalarArrayCellSources() {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src, bin := filepath.Join(root, "scalar-cell.fern"), filepath.Join(root, "scalar-cell")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if out, err := exec.Command(bin).CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			} else {
				assertBalancedCensus(t, string(out))
			}
		})
	}
}
