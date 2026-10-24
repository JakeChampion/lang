package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var assocGenericImplCases = []struct {
	name string
	src  string
	exit int
}{
	{"binding-typed-use-site", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    let b: Box[string] = Box { v: "hello" };
    return b.get().len();
}`, 5},
	{"bounded-generic-projection", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function unwrap[C: Carrier](c: C): C::Ok { return c.get(); }
function main(): i32 {
    let b: Box[i32] = Box { v: 20 };
    return b.get() + unwrap(b);
}`, 40},
	{"explicit-concrete-base", `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function twice(x: Box[i32]::Ok): i32 { return x + x; }
function main(): i32 {
    let b: Box[i32] = Box { v: 21 };
    return twice(b.get());
}`, 42},
	{"composite-binding", `trait Holder {
    type Item;
    function take(self: Self): Self::Item;
}
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = Option[T];
    function take(self: Self): Self::Item { return Some(self.v); }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 41 };
    let o: Option[i32] = b.take();
    match (o) { Some(v) => { return v + 1; }, None => { return 0; } }
}`, 42},
	{"parameters-bound-out-of-order", `trait Two {
    type A;
    type B;
    function fst(self: Self): Self::A;
    function snd(self: Self): Self::B;
}
struct P[X, Y] { a: X, b: Y }
impl[X, Y] Two for P[X, Y] {
    type A = Y;
    type B = X;
    function fst(self: Self): Self::A { return self.b; }
    function snd(self: Self): Self::B { return self.a; }
}
function main(): i32 {
    let p: P[string, i32] = P { a: "hi", b: 40 };
    return p.fst() + p.snd().len();
}`, 42},
}

func TestSelfHostAssocGenericImplX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range assocGenericImplCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostAssocGenericImplArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range assocGenericImplCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
