package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A grow through two records of a borrowed parameter clears a slot of the
// middle record when each box has one holder. A caller that keeps the
// record, the middle record or the array must see no change.
func TestSelfHostNestedFieldGrowKeepsTheCallersValues(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(nestedFieldGrowSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	cmd := exec.Command(cli, "-target", "x86-64-linux", "-o", out, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	output, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	got := string(output)
	if !strings.HasPrefix(got, nestedFieldGrowWant) {
		t.Fatalf("output:\n%s\nwant:\n%s", got, nestedFieldGrowWant)
	}
	if rest := strings.TrimPrefix(got, nestedFieldGrowWant); !strings.Contains(rest, "live_bytes=0") || strings.Count(rest, "\n") != 1 {
		t.Fatalf("the sanitizer reported:\n%s", rest)
	}
}

const nestedFieldGrowSource = `struct Ix { heads: i32[], count: i32 }
struct L { names: i32[], by: Ix }
struct Pair { a: L, b: L }
function (l: L) bound(n: i32): L {
  return L { names: l.names.append(n), by: Ix { heads: l.by.heads.with(n, l.by.count + 100), count: l.by.count + 1 } };
}
function sum(xs: i32[]): i32 {
  let t = 0;
  for x in xs { t = t + x; }
  return t;
}
function show(tag: string, l: L): void {
  print(tag + " names=" + l.names.len().to_string() + " heads=" + sum(l.by.heads).to_string() + " count=" + l.by.count.to_string());
}
function fresh(): L {
  return L { names: [], by: Ix { heads: [0, 0, 0, 0, 0, 0, 0, 0], count: 0 } };
}
// The caller keeps the record.
function keeps(): i32 {
  let l = fresh();
  let m = l.bound(1);
  show("keeps l", l);
  show("keeps m", m);
  return 0;
}
// The caller keeps only the middle record.
function keeps_mid(): i32 {
  let l = fresh();
  let b = l.by;
  let m = l.bound(2);
  show("mid m", m);
  print("mid b=" + sum(b.heads).to_string());
  return 0;
}
// The caller keeps only the array.
function keeps_array(): i32 {
  let l = fresh();
  let h = l.by.heads;
  let m = l.bound(3);
  show("arr m", m);
  print("arr h=" + sum(h).to_string());
  return 0;
}
// The same record goes two times.
function twice(): i32 {
  let l = fresh();
  let p = Pair { a: l.bound(4), b: l.bound(5) };
  show("twice a", p.a);
  show("twice b", p.b);
  show("twice l", l);
  return 0;
}
// A loop gives the same record each time.
function looped(): i32 {
  let l = fresh();
  let t = 0;
  for i in 0..4 {
    let m = l.bound(i);
    t = t + sum(m.by.heads);
  }
  show("loop l", l);
  print("loop t=" + t.to_string());
  return 0;
}
// The record dies at the call: the grow can write in place.
function chain(): i32 {
  let l = fresh();
  for i in 0..6 {
    l = l.bound(i);
  }
  show("chain", l);
  return 0;
}
// A borrowed parameter goes on to the grow.
function through(l: L, n: i32): L {
  return l.bound(n);
}
function via(): i32 {
  let l = fresh();
  let m = through(l, 6);
  show("via l", l);
  show("via m", m);
  let k = through(through(fresh(), 1), 2);
  show("via k", k);
  return 0;
}
// A middle record that two records share.
function shared_mid(): i32 {
  let ix = Ix { heads: [0, 0, 0, 0], count: 0 };
  let a = L { names: [], by: ix };
  let b = L { names: [], by: ix };
  let m = a.bound(1);
  show("shared m", m);
  show("shared b", b);
  print("shared ix=" + sum(ix.heads).to_string());
  return 0;
}
function main(): i32 {
  keeps(); keeps_mid(); keeps_array(); twice(); looped(); chain(); via(); shared_mid();
  return 0;
}
`

const nestedFieldGrowWant = `keeps l names=0 heads=0 count=0
keeps m names=1 heads=100 count=1
mid m names=1 heads=100 count=1
mid b=0
arr m names=1 heads=100 count=1
arr h=0
twice a names=1 heads=100 count=1
twice b names=1 heads=100 count=1
twice l names=0 heads=0 count=0
loop l names=0 heads=0 count=0
loop t=400
chain names=6 heads=615 count=6
via l names=0 heads=0 count=0
via m names=1 heads=100 count=1
via k names=2 heads=201 count=2
shared m names=1 heads=100 count=1
shared b names=0 heads=0 count=0
shared ix=0
`
