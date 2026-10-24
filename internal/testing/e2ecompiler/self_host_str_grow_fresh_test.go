package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const strGrowFreshSrc = `import "std/i32";

function build(n: i32): string {
  let s: string = "";
  let i: i32 = 0;
  while (i < n) {
    s = s + "x";
    i = i + 1;
  }
  return s;
}

function build_range(src: string, n: i32): string {
  let s: string = "";
  let i: i32 = 0;
  while (i < n) {
    s = s + slice_unchecked(src, i % 7, i % 7 + 3);
    i = i + 1;
  }
  return s;
}

// A snapshot shares the accumulator every seventh round, so the next append
// copies it into a fresh grown box: sized from what it holds, not from the
// capacity of the box it copies, or the capacity doubles each round.
function build_shared(n: i32): i32 {
  let s: string = "";
  let keep: string = "";
  let i: i32 = 0;
  while (i < n) {
    if (i % 7 == 0) {
      keep = s;
    }
    s = s + "ab";
    i = i + 1;
  }
  return s.len() + keep.len();
}

function main(): i32 {
  let a: string = build(3000);
  let b: string = build_range("abcdefghij", 1000);
  let kept: string = a;
  let c: string = a + "tail";
  a = a + "!";
  let bad: i32 = 0;
  if (a.len() != 3001 || kept.len() != 3000 || c.len() != 3004 || b.len() != 3000) {
    bad = bad + 1;
  }
  if (slice_unchecked(b, 0, 12) != "abcbcdcdedef" || slice_unchecked(a, 2998, 3001) != "xx!") {
    bad = bad + 2;
  }
  if (slice_unchecked(c, 2999, 3004) != "xtail" || slice_unchecked(kept, 2999, 3000) != "x") {
    bad = bad + 4;
  }
  if (build_shared(300) != 1188) {
    bad = bad + 8;
  }
  return bad;
}
`

const strGrowFreshAllocs = 80

func TestSelfHostStrGrowFreshX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", strGrowFreshSrc)); code != 0 {
		t.Fatalf("exit %d, want 0 (bit 1: a length, 2: the grown bytes, 4: the kept string's bytes, 8: the shared accumulator)", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", strGrowFreshSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	if allocs := allocsFromLeakcheck(t, "x86-64", stderr); allocs > strGrowFreshAllocs {
		t.Errorf("allocs=%d, want at most %d: a self-append without room built an exact box instead of a grown one", allocs, strGrowFreshAllocs)
	}
	quarantine := buildBin(t, cli.gcc, t.TempDir(), "quarantine", cli.emit(t, "x86-64-linux", strGrowFreshSrc, "FERN_RC_FREE_DEBUG=1"))
	if stderr, exit := runWithStdin(t, cli.runner, quarantine, nil); exit != 0 {
		t.Fatalf("quarantine run: exit %d, want 0\n%s", exit, stderr)
	}
}

func TestSelfHostStrGrowFreshArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", strGrowFreshSrc)); code != 0 {
		t.Fatalf("arm64: exit %d, want 0", code)
	}
	bin := buildBinArm64(t, gcc, t.TempDir(), "census", cli.emit(t, "arm64-linux", strGrowFreshSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, []string{qemu}, bin, nil)
	if exit != 0 {
		t.Fatalf("arm64 census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	if allocs := allocsFromLeakcheck(t, "arm64", stderr); allocs > strGrowFreshAllocs {
		t.Errorf("arm64: allocs=%d, want at most %d", allocs, strGrowFreshAllocs)
	}
}

func TestSelfHostStrGrowFreshWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", strGrowFreshSrc)); code != 0 {
		t.Fatalf("wasm: exit %d, want 0", code)
	}
	wat := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(wat, []byte(cli.emit(t, "wasm32-wasi", strGrowFreshSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, wat)
	if exit != 0 {
		t.Fatalf("wasm census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	if allocs := allocsFromLeakcheck(t, "wasm", stderr); allocs > strGrowFreshAllocs {
		t.Errorf("wasm: allocs=%d, want at most %d", allocs, strGrowFreshAllocs)
	}
}
