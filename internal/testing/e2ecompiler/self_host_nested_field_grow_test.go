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
	// The second program gives the middle record to a callee that does the
	// write, and that the compiler does not splice.
	handed := strings.Replace(nestedFieldGrowSource, nestedFieldGrowInline, nestedFieldGrowHanded, 1)
	if handed == nestedFieldGrowSource {
		t.Fatal("the source does not have the body to replace")
	}
	for _, tc := range []struct{ name, src, want string }{
		{"the function does the write", nestedFieldGrowSource, nestedFieldGrowWant},
		{"a callee does the write", handed, nestedFieldGrowWant},
		{"the frame needs the record after the write", nestedFieldGrowAfterSource, nestedFieldGrowAfterWant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
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
			if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("output:\n%s\nwant:\n%s", got, tc.want)
			}
			if rest := strings.TrimPrefix(got, tc.want); !strings.Contains(rest, "live_bytes=0") || strings.Count(rest, "\n") != 1 {
				t.Fatalf("the sanitizer reported:\n%s", rest)
			}
		})
	}
}

const nestedFieldGrowInline = `function (l: L) bound(n: i32): L {
  return L { names: l.names.append(n), by: Ix { heads: l.by.heads.with(n, l.by.count + 100), count: l.by.count + 1 } };
}`

const nestedFieldGrowHanded = `function (ix: Ix) entered(n: i32): Ix {
  let t = 0;
  for k in 0..3 {
    t = t + k;
  }
  return Ix { heads: ix.heads.with(n, ix.count + 97 + t), count: ix.count + 1 };
}
function (l: L) bound(n: i32): L {
  return L { names: l.names.append(n), by: l.by.entered(n) };
}`

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

// The frame reads the record, gives it on, or returns its middle record
// after the write, so the write must not clear a slot that it still needs.
const nestedFieldGrowAfterSource = `struct Ix { heads: i32[], count: i32 }
struct L { names: i32[], by: Ix }
function (ix: Ix) entered(n: i32): Ix {
  let t = 0;
  for k in 0..3 {
    t = t + k;
  }
  return Ix { heads: ix.heads.with(n, ix.count + 97 + t), count: ix.count + 1 };
}
function sum(xs: i32[]): i32 {
  let t = 0;
  for x in xs { t = t + x; }
  return t;
}
function total(l: L): i32 {
  return sum(l.by.heads) + l.by.count + l.names.len();
}
function fresh(): L {
  return L { names: [7], by: Ix { heads: [1, 1, 1, 1, 1, 1, 1, 1], count: 0 } };
}
// The frame reads the array again after the call.
function reads_after(l: L, n: i32): i32 {
  let m = l.by.entered(n);
  return sum(l.by.heads) * 1000 + sum(m.heads);
}
// The frame gives the whole record on after the call.
function passes_after(l: L, n: i32): i32 {
  let m = l.by.entered(n);
  return total(l) * 1000 + sum(m.heads);
}
// The frame returns the middle record that it read before the call.
function returns_mid(l: L, n: i32): Ix {
  let b = l.by;
  let m = l.by.entered(n);
  if (m.count > 100) {
    return m;
  }
  return b;
}
// The frame writes in place and reads the parameter after that.
function writes_then_reads(l: L, n: i32): i32 {
  let h = l.by.heads.with(n, 50);
  return sum(h) * 1000 + total(l);
}
// The same write in each pass of a loop.
function writes_in_loop(l: L): i32 {
  let t = 0;
  for i in 0..4 {
    let h = l.by.heads.with(i, 50);
    t = t + sum(h);
  }
  return t * 1000 + total(l);
}
// The frame gives the field two times.
function hands_twice(l: L): i32 {
  let a = l.by.entered(1);
  let b = l.by.entered(2);
  return sum(a.heads) * 1000 + sum(b.heads);
}
function main(): i32 {
  print("reads_after " + reads_after(fresh(), 2).to_string());
  print("passes_after " + passes_after(fresh(), 2).to_string());
  print("returns_mid " + sum(returns_mid(fresh(), 2).heads).to_string());
  print("writes_then_reads " + writes_then_reads(fresh(), 2).to_string());
  print("writes_in_loop " + writes_in_loop(fresh()).to_string());
  print("hands_twice " + hands_twice(fresh()).to_string());
  let l = fresh();
  print("kept " + reads_after(l, 3).to_string() + " " + total(l).to_string());
  return 0;
}
`

const nestedFieldGrowAfterWant = `reads_after 8107
passes_after 9107
returns_mid 8
writes_then_reads 57009
writes_in_loop 228009
hands_twice 107107
kept 8107 9
`
