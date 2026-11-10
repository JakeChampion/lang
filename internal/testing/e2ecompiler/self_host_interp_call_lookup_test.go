package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A call, a method call and an in-place append in fern -interp cost the same
// whether the program declares a few functions or thousands: the interpreter
// finds a function by its name, not by a scan of every declaration.
func TestSelfHostInterpCallCostIgnoresFunctionCount(t *testing.T) {
	cli := buildSelfHostCLI(t)
	small := runInterpTimed(t, cli, callLoopProgram(0))
	large := runInterpTimed(t, cli, callLoopProgram(4000))
	t.Logf("no filler functions: %v, 4000 filler functions: %v", small, large)
	if large > 4*small {
		t.Fatalf("4000 extra functions made the call loop %.1f times slower (%v against %v)", float64(large)/float64(small), large, small)
	}
}

func callLoopProgram(fillers int) string {
	var b strings.Builder
	for i := 0; i < fillers; i++ {
		fmt.Fprintf(&b, "function filler%d(x: i32): i32 {\n  return x + %d;\n}\n\n", i, i)
	}
	b.WriteString(`struct Acc { total: i32 }

function (a: Acc) add(x: i32): Acc {
  return Acc { total: a.total + x };
}

function step(i: i32): i32 {
  return i & 7;
}

function main(): i32 {
  let xs: i32[] = [];
  let acc = Acc { total: 0 };
  let i: i32 = 0;
  while (i < 300000) {
    let v = step(i);
    acc = acc.add(v);
    xs = xs.append(v);
    i = i + 1;
  }
  print(xs.len());
  print(acc.total);
  return 0;
}
`)
	return b.String()
}

func runInterpTimed(t *testing.T, cli *selfHostCLI, prog string) time.Duration {
	t.Helper()
	src := filepath.Join(t.TempDir(), "call_loop.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	const budget = 5 * time.Minute
	cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(budget, func() { cmd.Process.Kill() })
	err := cmd.Wait()
	timer.Stop()
	elapsed := time.Since(start)
	if elapsed >= budget {
		t.Fatalf("fern -interp took longer than %v", budget)
	}
	if want := "300000\n1050000\n"; err != nil || out.String() != want {
		t.Fatalf("fern -interp: %v, output %q; want %q", err, out.String(), want)
	}
	return elapsed
}
