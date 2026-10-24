package e2ecompiler

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

const bigintChainSrc = `import "core/bigint";

struct LD { m: bigint.BigInt, e: i32 }

function add(a: LD, b: LD): LD {
  let e: i32 = a.e;
  if (b.e < e) {
    e = b.e;
  }
  let av: bigint.BigInt = a.m.shl(a.e - e);
  let bv: bigint.BigInt = b.m.shl(b.e - e);
  let s: bigint.BigInt = av.add(bv);
  let kept: bigint.BigInt = s.shr(1);
  let dropped: bigint.BigInt = s.sub(kept.shl(1));
  if (!dropped.is_zero()) {
    kept = kept.add(bigint.from_i64(1 as i64));
  }
  return LD { m: kept, e: e + 1 };
}

function main(): i32 {
  let x: LD = LD { m: bigint.from_i64(12345678901234567 as i64), e: 0 };
  let step: LD = LD { m: bigint.from_i64(1000 as i64), e: 3 };
  let i: i32 = 0;
  while (i < 2000) {
    x = add(x, step);
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 99; }
  return x.m.bit_length() % 100;
}
`

const bigintChainExit = 50

var seqLongDoubleArgs = []string{"1e29", "1e26", "2.2e29"}

func TestSelfHostBigintChainReclaimX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	seqSrc, err := filepath.Abs("../../../coreutils/seq.fern")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, bin string, args ...string) (stdout, stderr string, exit int) {
		t.Helper()
		cmd := runX86_64Bin(cli.runner, bin, args...)
		var ob, eb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &ob, &eb
		_ = cmd.Run()
		return ob.String(), eb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("ld-add", func(t *testing.T) {
		src := mustWrite(t, t.TempDir(), "main.fern", bigintChainSrc)
		_, stderr, exit := run(t, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"))
		if exit != bigintChainExit {
			t.Fatalf("exit = %d, want %d (99 = rc underflow)\n%s", exit, bigintChainExit, stderr)
		}
		allocs, frees, live := leakSummaryOf(t, "ld-add", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Fatalf("allocs=%d frees=%d live_bytes=%d, want balanced", allocs, frees, live)
		}
	})

	t.Run("seq-long-double", func(t *testing.T) {
		stdout, stderr, exit := run(t, cli.x86Binary(t, seqSrc, "FERN_LEAKCHECK=1"), seqLongDoubleArgs...)
		if exit != 0 {
			t.Fatalf("exit = %d\n%s", exit, stderr)
		}
		natBin := filepath.Join(t.TempDir(), "seq_native")
		if out, err := exec.Command(buildLangBinForInterp(t), "-target", "x86-64-linux", "-o", natBin, seqSrc).CombinedOutput(); err != nil {
			t.Fatalf("native build: %v\n%s", err, out)
		}
		want, _, _ := run(t, natBin, seqLongDoubleArgs...)
		if stdout != want {
			t.Fatalf("self-host seq output differs from native (%d vs %d bytes)", len(stdout), len(want))
		}
		allocs, frees, live := leakSummaryOf(t, "seq", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Fatalf("allocs=%d frees=%d live_bytes=%d, want balanced", allocs, frees, live)
		}
	})
}

func TestSelfHostBigintChainReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm bigint chain reclaim")
	}
	cli := buildSelfHostCLI(t)
	seqSrc, err := filepath.Abs("../../../coreutils/seq.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, src string
		args      []string
		want      int
	}{
		{"ld-add", "", nil, bigintChainExit},
		{"seq-long-double", seqSrc, seqLongDoubleArgs, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			if src == "" {
				src = mustWrite(t, t.TempDir(), "main.fern", bigintChainSrc)
			}
			stderr, code := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"), tc.args...)
			if code != tc.want {
				t.Fatalf("exit = %d, want %d (99 = rc underflow)\n%s", code, tc.want, stderr)
			}
			allocs, frees, live := leakSummaryOf(t, tc.name, stderr)
			if allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live_bytes=%d, want balanced", allocs, frees, live)
			}
		})
	}
}
