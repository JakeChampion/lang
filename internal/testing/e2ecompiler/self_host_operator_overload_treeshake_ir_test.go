package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var operatorOverloadRootCases = []struct {
	name string
	src  string
}{
	{"binary_mul_returns_scalar", `struct V { v: i32 }

function (a: V) mul(b: V): i32 {
    return a.v * b.v;
}

function main(): i32 {
    let x: V = V { v: 6 };
    let y: V = V { v: 7 };
    return x * y;
}`},
	{"binary_add_returns_string", `struct S { s: string }

function (a: S) add(b: S): string {
    return a.s;
}

function main(): i32 {
    let p: S = S { s: "hello" };
    let q: S = S { s: "world" };
    if ((p + q).len() == 5) { return 42; }
    return 1;
}`},
	{"unary_neg_returns_scalar", `struct N { n: i32 }

function (a: N) neg(): i32 {
    return a.n;
}

function main(): i32 {
    let k: N = N { n: 42 };
    return -k;
}`},
	{"binary_add_returns_self", `struct C { n: i32 }

function (a: C) add(b: C): C {
    return C { n: a.n + b.n };
}

function main(): i32 {
    let u: C = C { n: 40 };
    let v: C = C { n: 2 };
    return (u + v).n;
}`},
}

func TestSelfHostOperatorOverloadRootsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range operatorOverloadRootCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			ccmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot)
			ccmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, cerr := ccmd.CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
