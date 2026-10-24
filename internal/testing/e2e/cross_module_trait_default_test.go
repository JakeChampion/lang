package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const crossModuleTraitDefaultLib = `pub trait It[T] {
  function next(self: Self): Option[(T, Self)];
  function fold[A](self: Self, init: A, f: (A, T) => A): A {
    let acc: A = init;
    let cur: Self = self;
    let go: boolean = true;
    while (go) {
      match (cur.next()) {
        Some(t) => { acc = f(acc, t.0); cur = t.1; },
        None => { go = false; }
      }
    }
    return acc;
  }
  function map[U](self: Self, f: (T) => U): Mapped[Self, T, U] {
    return Mapped { it: self, f: f };
  }
}

pub struct Mapped[I, T, U] { it: I, f: (T) => U }

impl[I: It[T], T, U] It[U] for Mapped[I, T, U] {
  function next(self: Mapped[I, T, U]): Option[(U, Mapped[I, T, U])] {
    match (self.it.next()) {
      Some(t) => { return Some((self.f(t.0), Mapped { it: t.1, f: self.f })); },
      None => { return None; }
    }
  }
}
`

const crossModuleTraitDefaultMain = `import "./lib";
import "std/i32";

pub struct Countdown { n: i32 }
impl lib.It[i32] for Countdown {
  function next(self: Countdown): Option[(i32, Countdown)] {
    if (self.n <= 0) { return None; }
    return Some((self.n, Countdown { n: self.n - 1 }));
  }
}

function main(): i32 {
  print(Countdown { n: 5 }.fold(0, (a: i32, b: i32): i32 => a * 10 + b).to_string());
  print(Countdown { n: 4 }.map((i: i32): string => i.to_string()).fold("", (a: string, s: string): string => a + s + ";"));
  return 0;
}
`

const crossModuleTraitDefaultWant = "54321\n4;3;2;1;\n"

func writeCrossModuleTraitDefaultProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(crossModuleTraitDefaultLib), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(crossModuleTraitDefaultMain), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "main.fern")
}

func TestInterpCrossModuleTraitDefault(t *testing.T) {
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), writeCrossModuleTraitDefaultProject(t))
	if code != 0 || out != crossModuleTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, crossModuleTraitDefaultWant, errOut)
	}
}

func TestX86_64CrossModuleTraitDefault(t *testing.T) {
	if out, code := runFixtureX86_64(t, writeCrossModuleTraitDefaultProject(t), ""); code != 0 || out != crossModuleTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, crossModuleTraitDefaultWant)
	}
}

func TestArm64CrossModuleTraitDefault(t *testing.T) {
	if out, code := runFixtureArm64(t, writeCrossModuleTraitDefaultProject(t), ""); code != 0 || out != crossModuleTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, crossModuleTraitDefaultWant)
	}
}

func TestWasmCrossModuleTraitDefault(t *testing.T) {
	if out, code := runFixtureWasm(t, writeCrossModuleTraitDefaultProject(t), ""); code != 0 || out != crossModuleTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, crossModuleTraitDefaultWant)
	}
}
