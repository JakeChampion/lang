package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The emit carries only the bodies reachable from the program's roots
// (#12241). The tree shake keeps every `get` for a call on a trait-bounded
// type parameter, so `B.get` survives it though only `read[A]` is
// instantiated; the reachability prune drops it. A function reached only as a value, only from
// inside a capture-free closure, or only through a dyn dispatch is still
// emitted and still runs.
func TestSelfHostEmitDropsUnreachedBodies(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "reach.fern")
	prog := `struct A { x: i32 }
struct B { x: i32 }
struct C { k: i32 }

trait Getter {
  function get(self: Self): i32;
}

impl Getter for A {
  function get(self: Self): i32 {
    return self.x;
  }
}

impl Getter for B {
  function get(self: Self): i32 {
    return self.x * 7 + 1;
  }
}

trait Scale {
  function scale(self: Self, x: i32): i32;
}

impl Scale for C {
  function scale(self: Self, x: i32): i32 {
    return x * self.k;
  }
}

@noinline function read[T: Getter](v: T): i32 {
  return v.get();
}

@noinline function triple(x: i32): i32 {
  return x * 3;
}

@noinline function bump(x: i32): i32 {
  return x + 1;
}

function main(): i32 {
  let a: A = A { x: 3 };
  let f: (i32) => i32 = triple;
  let g: (i32) => i32 = (x: i32): i32 => bump(x);
  let s: dyn Scale = C { k: 2 };
  return s.scale(g(f(read(a))));
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		asmPath := filepath.Join(dir, "reach-"+tg.target+".s")
		args := []string{"-target", tg.target, "-emit", "asm", "-o", asmPath, src, h.stdlib}
		if out, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", tg.target, err, out)
		}
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatal(err)
		}
		if functionListing(string(asm), "__fn_B__get") != "" {
			t.Errorf("%s: B.get is emitted though nothing reaches it", tg.target)
		}
		for _, kept := range []string{"__fn_read__A", "__fn_triple", "__fn_bump", "__fn_C__scale"} {
			if functionListing(string(asm), kept) == "" {
				t.Errorf("%s: %s is missing though the program reaches it", tg.target, kept)
			}
		}
		bin := filepath.Join(dir, "reach-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 20 {
			t.Errorf("%s: exit %d, want 20", tg.target, code)
		}
	}
}

// A `fip` claim is checked whether or not the program reaches it: `B.get`
// survives the tree shake by name, nothing reaches it, and its fresh
// construction still draws E068.
func TestSelfHostReachPruneChecksUnreachedFipClaim(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "fip.fern")
	prog := `struct A { x: i32 }
struct B { x: i32 }
struct P { x: i32 }

trait Getter {
  function get(self: Self): i32;
}

impl Getter for A {
  function get(self: Self): i32 {
    return self.x;
  }
}

fip function (b: B) get(): P {
  return P { x: b.x };
}

function read[T: Getter](v: T): i32 {
  return v.get();
}

function main(): i32 {
  return read(A { x: 3 });
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		asmPath := filepath.Join(dir, "fip-"+tg.target+".s")
		args := []string{"-target", tg.target, "-emit", "asm", "-o", asmPath, src, h.stdlib}
		out, err := exec.Command(h.cli, args...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "E068") {
			t.Errorf("%s: the unreached fip claim was not refused (err %v):\n%s", tg.target, err, out)
		}
	}
}
