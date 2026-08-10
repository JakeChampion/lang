// #4495: a `dyn Trait` local declared inside a branch that doesn't run (or a
// loop-var dyn on its FIRST iteration's reinit drop) must be swept as NULL,
// not as stack garbage. The exit dec sweep visits every declared local
// whether or not its `var` ran, and native stack slots hold garbage — the
// per-set __drop_dyn_<set> helper NULL-guards the cell precisely on the
// assumption the entry safety-zero covers dyn slots, which it didn't:
// pre-fix, both programs segfault on x86-64 whenever the stack region is
// dirty (the `dirty()` call below makes that deterministic).
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const dynZeroPrelude = `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
function dirty(): i32 {
    var a: i32 = 1094795585;
    var b: i32 = 1431655765;
    var c: i32 = 1094795585;
    var d: i32 = 1431655765;
    var e: i32 = 1094795585;
    var f: i32 = 1431655765;
    return a + b + c + d + e + f;
}
`

func runDynZeroProg(t *testing.T, name, src string, wantExit int) {
	t.Helper()
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	p := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, name+".bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build: %v\n%s", err, o)
	}
	cmd := runX86Bin(qemu, out)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != wantExit {
		t.Errorf("exit = %d, want %d (a segfault here means the dyn slot was swept uninitialised)", code, wantExit)
	}
}

// Conditionally-declared dyn local: the false path still sweeps the slot.
func TestX86_64DynCondDeclSweepsNull(t *testing.T) {
	runDynZeroProg(t, "dyn_cond", dynZeroPrelude+`
function f(cond: boolean): i32 {
    if (cond) {
        var d: dyn Shape = Circle { r: 5 };
        print(d.area().to_string());
    }
    return 7;
}
function main(): i32 {
    var x: i32 = dirty();
    var y: i32 = f(false);
    if (x != 0 && y == 7) { return 7; }
    return 1;
}
`, 7)
}

// Loop-var dyn: the FIRST iteration's re-declaration reinit drop reads the
// slot before anything was ever stored to it.
func TestX86_64DynLoopVarFirstReinitSweepsNull(t *testing.T) {
	runDynZeroProg(t, "dyn_loop", dynZeroPrelude+`
function g(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 3) {
        var d: dyn Shape = Circle { r: i + 1 };
        acc = acc + d.area();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = dirty();
    if (x == 0) { return 1; }
    return g();
}
`, 14)
}
