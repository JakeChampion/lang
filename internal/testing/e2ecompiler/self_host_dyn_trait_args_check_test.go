package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A method called through a `dyn` value returns what its trait declares, read
// at the trait arguments and associated-type pins the dyn spelling gives:
// `T` in `dyn Tc[i32]` is i32, and `Self::Item` in `dyn P[Item = i32]` is
// i32. The self-host checker left both unknown, so -check refused a program
// that used the result in arithmetic, and could not see a result used at the
// wrong type. Found by TestDifferential_CheckerMutants. A call's arguments are
// read at the same spelling and checked against it (#12639).
func TestSelfHostDynTraitArgsCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	const decls = `trait P { type Item; function get(self: Self): Self::Item; function opt(self: Self): Option[Self::Item]; }
trait Tc[T] { function tc(self: Self, d: T): T; }
struct B { v: i32 }
impl P for B { type Item = i32; function get(self: Self): Self::Item { return self.v; } function opt(self: Self): Option[Self::Item] { return Some(self.v); } }
impl Tc[i32] for B { function tc(self: Self, d: i32): i32 { return self.v + d; } }
`
	cases := []struct {
		name, body string
		ok         bool
	}{
		{"pinned result in arithmetic", "function f(d: dyn P[Item = i32]): i32 { return d.get() * 2; }", true},
		{"trait argument in a bitwise op", "function f(d: dyn Tc[i32]): i32 { return d.tc(3) ^ 7; }", true},
		{"pin inside the result", "function f(d: dyn P[Item = i32]): i32 { match (d.opt()) { Some(x) => { return x + 1; }, None => { return 0; } } }", true},
		{"pinned result at the wrong type", "function f(d: dyn P[Item = i32]): i32 { let s: string = d.get(); return s.len(); }", false},
		{"trait argument at the wrong type", "function f(d: dyn Tc[i32]): i32 { let s: string = d.tc(3); return s.len(); }", false},
		{"argument at the trait argument", "function f(d: dyn Tc[i32]): i32 { let x: i32 = 3; return d.tc(x); }", true},
		{"argument against the trait argument", "function f(d: dyn Tc[i32]): i32 { return d.tc(\"three\"); }", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			prog := decls + tc.body + "\nfunction main(): i32 { return f(B { v: 4 }); }\n"
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			nativeOut, nativeErr := exec.Command(nativeBin, "-check", src).CombinedOutput()
			shOut, shErr := exec.Command(driverBin, "-check", src, stdlib).CombinedOutput()
			if (nativeErr == nil) != tc.ok {
				t.Errorf("Go checker accepts = %v, want %v\n%s", nativeErr == nil, tc.ok, nativeOut)
			}
			if (shErr == nil) != tc.ok {
				t.Errorf("self-host checker accepts = %v, want %v\n%s", shErr == nil, tc.ok, shOut)
			}
		})
	}
}
