package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const packedAggregateRetentionProgram = `import "std/string";
enum Saved { Empty, Entry(u8, boolean, i32, string) }
enum Getter { NoGetter, WithGetter(u8, boolean, i32, () => string) }
struct Sink { save: (string, i32) => Getter }

@noinline function fresh(unit: string): string { return unit.repeat(12); }
@noinline function capture(s: string): () => string { return (): string => s; }
@noinline function fill(sink: Sink, unit: string, n: i32): Getter {
  let src = fresh(unit);
  return sink.save(slice_unchecked(src, 2, 10), n);
}
@noinline function churn(): i32 {
  let n = 0;
  for i in 0..256 { let junk = fresh("zq"); n += junk.len(); }
  return n;
}
@noinline function reflag(v: Saved): Saved {
  return match (v) {
    Entry(tag, flag, n, text) => Entry(tag, !flag, n, text),
    Empty => Empty,
  };
}
@noinline function verify(v: Saved, want: string, number: i32, flag: boolean): i32 {
  match (v) {
    Entry(tag, actualFlag, n, text) => {
      if (tag != 231u8 || actualFlag != flag) { return 41; }
      if (n != number) { return 42; }
      if (text != want) { return 43; }
      return 0;
    },
    Empty => { return 45; },
  }
}
@noinline function verifyGetter(v: Getter, want: string, number: i32): i32 {
  match (v) {
    WithGetter(tag, flag, n, get) => {
      if (tag != 231u8 || !flag || n != number) { return 50; }
      if (get() != want) { return 51; }
      return 0;
    },
    NoGetter => { return 52; },
  }
}
function main(): i32 {
  let initial: Saved = Empty;
  let storage = cell_new(initial);
  let sink = Sink { save: (s: string, n: i32): Getter => {
    storage.set(Entry(231u8, true, n, s));
    return WithGetter(231u8, true, n, capture(s));
  } };
  let firstGetter = fill(sink, "ab", -2147483648);
  let first = storage.get();
  let alias = first;
  let secondGetter = fill(sink, "cd", 2147483647);
  let second = storage.get();
  let changed = reflag(first);
  storage.set(Empty);
  if (churn() != 6144) { return 48; }
  let a = verify(first, "abababab", -2147483648, true);
  if (a != 0) { return a; }
  let b = verify(alias, "abababab", -2147483648, true);
  if (b != 0) { return b; }
  let c = verify(changed, "abababab", -2147483648, false);
  if (c != 0) { return c; }
  let d = verify(second, "cdcdcdcd", 2147483647, true);
  if (d != 0) { return d; }
  let e = verifyGetter(firstGetter, "abababab", -2147483648);
  if (e != 0) { return e; }
  let f = verifyGetter(secondGetter, "cdcdcdcd", 2147483647);
  if (f != 0) { return f; }
  if (__rc_underflow_count() != 0) { return 49; }
  print("packed aggregate views survive");
  return 0;
}
`

func TestSelfHostPackedAggregateRetention(t *testing.T) {
	cli := buildRuntimeCLI(t)
	const want = "packed aggregate views survive\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				got, stderr := runLentView(t, cli, packedAggregateRetentionProgram, target, sanitize)
				if got != want {
					t.Fatalf("sanitize=%v: got %q, want %q", sanitize, got, want)
				}
				if sanitize {
					assertBalancedCensus(t, stderr)
					t.Logf("%s", leakSummaryLine(stderr))
				}
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target+"-harden", func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				flags := []string{"-harden"}
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, packedAggregateRetentionProgram, target, "", flags...)
				cmd := runX86_64Bin(cli.runner, bin)
				if target == "arm64-linux" {
					_, qemu := arm64Tooling(t)
					cmd = runArm64Bin(qemu, bin)
				}
				got, stderr := runCPUProfile(t, cmd, 0)
				if got != want {
					t.Fatalf("sanitize=%v: got %q, want %q", sanitize, got, want)
				}
				if sanitize {
					assertBalancedCensus(t, stderr)
					t.Logf("%s", leakSummaryLine(stderr))
				}
			}
		})
	}
}

const packedFunctionCellRefusalProgram = `enum Refused { None, Payload(u8, boolean, i32, () => string) }
function main(): i32 {
  let value: Refused = None;
  let storage = cell_new(value);
  return 0;
}
`

func TestSelfHostPackedFunctionCellRefusal(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(packedFunctionCellRefusalProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	native := buildFernCLIBin(t)
	out, err := exec.Command(native, "-check", src).CombinedOutput()
	if err == nil {
		t.Fatalf("Go checker accepts a cell with a function field: %s", out)
	}
	cli := newStrictCLI(t)
	_, stderr, err := cli.tryEmit(t, "x86-64-linux", packedFunctionCellRefusalProgram)
	if err == nil {
		t.Fatalf("self-hosted checker accepts a cell with a function field: %s", stderr)
	}
	for _, diagnostic := range []string{string(out), stderr} {
		if strings.Count(diagnostic, "error[E057]") != 1 || strings.Count(diagnostic, "error[") != 1 {
			t.Errorf("want exactly one E057: %s", diagnostic)
		}
		for _, fragment := range []string{"Cell[Refused]", "contains () => string"} {
			if !strings.Contains(diagnostic, fragment) {
				t.Errorf("want diagnostic fragment %q: %s", fragment, diagnostic)
			}
		}
	}
}
