package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Appending a million elements one at a time finishes well inside the budget:
// each append extends the array in place rather than copying it (#13394).
func TestSelfHostInterpAppendIsLinear(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "append_million.fern")
	prog := `function main(): i32 {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 1000000) {
    xs = xs.append(i);
    i = i + 1;
  }
  print(xs.len());
  print(xs[0] + xs[999999]);
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	const budget = 2 * time.Minute
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
		t.Fatalf("fern -interp took longer than %v to append 1,000,000 elements", budget)
	}
	if want := "1000000\n999999\n"; err != nil || out.String() != want {
		t.Fatalf("fern -interp: %v, output %q; want %q", err, out.String(), want)
	}
	t.Logf("1,000,000 appends in %v", elapsed)
}

// Arrays keep value semantics however they are shared: appending to one
// holder never shows in another, whether the other is a binding, a struct
// field, a closure capture, an argument, an element of another array, or an
// earlier state of the same binding. An array whose length a builtin set
// appends after that length.
func TestSelfHostInterpAppendKeepsValueSemantics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildFernCLIBin(t)
	src := filepath.Join(t.TempDir(), "append_aliases.fern")
	prog := `struct Holder { xs: i32[] }

function grow(p: i32[], v: i32): i32[] {
  return p.append(v);
}

function show(label: string, xs: i32[]): void {
  let s: string = label + ":";
  for x in xs {
    s = s + " " + x.to_string();
  }
  print(s);
}

function show_bytes(label: string, xs: u8[]): void {
  let s: string = label + ":";
  for x in xs {
    s = s + " " + x.to_string();
  }
  print(s);
}

function main(): i32 {
  let a: i32[] = [1, 2];
  let b: i32[] = a;
  b = b.append(3);
  a = a.append(4);
  show("a", a);
  show("b", b);

  let base: i32[] = [7];
  let h: Holder = Holder { xs: base };
  base = base.append(8);
  h = Holder { ...h, xs: h.xs.append(9) };
  let h2: Holder = h;
  h2 = Holder { ...h2, xs: h2.xs.append(10) };
  show("base", base);
  show("h.xs", h.xs);
  show("h2.xs", h2.xs);

  let c: i32[] = [10];
  let add = (v: i32): i32[] => { return c.append(v); };
  let c1: i32[] = add(11);
  let c2: i32[] = add(12);
  c = c.append(13);
  show("c", c);
  show("c1", c1);
  show("c2", c2);

  let d: i32[] = [20, 21];
  let e: i32[] = grow(d, 22);
  let g: i32[] = grow(d, 23);
  d = d.append(24);
  show("d", d);
  show("e", e);
  show("g", g);

  let inner: i32[] = [30];
  let outer: i32[][] = [inner];
  inner = inner.append(31);
  outer = outer.append(inner);
  inner = inner.append(32);
  show("outer[0]", outer[0]);
  show("outer[1]", outer[1]);
  show("inner", inner);

  let xs: i32[] = [];
  let snap: i32[] = [];
  let i: i32 = 0;
  while (i < 5) {
    xs = xs.append(i);
    if (i == 2) {
      snap = xs;
    }
    i = i + 1;
  }
  snap = snap.append(99);
  xs = xs.append(5);
  show("snap", snap);
  show("xs", xs);

  let buf: u8[] = [1u8, 2u8, 3u8, 4u8];
  let held: u8[] = buf;
  __arr_set_len(buf, 2);
  buf = buf.append(9u8);
  held = held.append(7u8);
  show_bytes("buf", buf);
  show_bytes("held", held);
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "a: 1 2 4\nb: 1 2 3\n" +
		"base: 7 8\nh.xs: 7 9\nh2.xs: 7 9 10\n" +
		"c: 10 13\nc1: 10 11\nc2: 10 12\n" +
		"d: 20 21 24\ne: 20 21 22\ng: 20 21 23\n" +
		"outer[0]: 30\nouter[1]: 30 31\ninner: 30 31 32\n" +
		"snap: 0 1 2 99\nxs: 0 1 2 3 4 5\n" +
		"buf: 1 2 9\nheld: 1 2 7\n"
	sOut, sErr, sCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib), "")
	oOut, oErr, oCode := runInterpCLI(t, exec.Command(oracle, "-interp", src), "")
	xOut, xErr, xCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), "")
	if xOut != want || xCode != 0 {
		t.Fatalf("x86-64: exit %d, stdout %q (stderr %q); want %q", xCode, xOut, xErr, want)
	}
	if sOut != want || sCode != 0 {
		t.Errorf("fern -interp: exit %d, stdout %q (stderr %q); want %q", sCode, sOut, sErr, want)
	}
	if oOut != want || oCode != 0 {
		t.Errorf("fern-oracle -interp: exit %d, stdout %q (stderr %q); want %q", oCode, oOut, oErr, want)
	}
}
