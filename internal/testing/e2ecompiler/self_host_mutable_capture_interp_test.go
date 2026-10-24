package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostMutableScalarCaptureInterp(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	interpDriver := buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"write-only-capture", `function main(): i32 { let x = 1; let f = (): i32 => { x = 42; return 7; }; let r = f(); return r + x; }`},
		{"counter", `function main(): i32 { let x = 0; let inc = (): i32 => { x = x + 1; return x; }; inc(); inc(); return x; }`},
		{"two-closures-share", `function main(): i32 { let n: i32 = 0; let a: () => i32 = (): i32 => { n = n + 1; return n; }; let b: () => i32 = (): i32 => { n = n + 10; return n; }; a(); b(); return n; }`},
		{"bool-capture", `function main(): i32 { let b: boolean = false; let f: () => i32 = (): i32 => { b = true; return 0; }; f(); if (b) { return 7; } return 0; }`},

		{"inner-shadow-control", `function main(): i32 { let x: i32 = 1; let f: () => i32 = (): i32 => { let x: i32 = 5; x = x + 1; return x; }; let r = f(); return r + x; }`},
		{"read-only-capture-control", `function main(): i32 { let k: i32 = 40; let f: () => i32 = (): i32 => { return k + 2; }; return f(); }`},
		{"string-capture-control", `function main(): i32 { let s: string = "abcd"; let f: () => i32 = (): i32 => { return s.len(); }; return f(); }`},
		{"array-capture-control", `function main(): i32 { let xs: i32[] = [1,2,3]; let f: () => i32 = (): i32 => { return xs[2]; }; return f(); }`},
		{"no-lambda-assign-control", `function main(): i32 { let x: i32 = 1; x = 41; return x + 1; }`},
		{"loop-counter-control", `function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 5) { t = t + i; i = i + 1; } return t; }`},

		{"outer-write-toplevel", `function main(): i32 { let i: i32 = 0; let f: (i32) => i32 = ((x: i32) => x + i); i = 3; return f(0); }`},
		{"outer-write-in-loop", `function main(): i32 { let fs: ((i32) => i32)[] = []; let i: i32 = 0; while (i < 3) { let g: (i32) => i32 = ((x: i32) => x + i); fs = fs.append(g); i = i + 1; } return (fs[0])(0); }`},
		{"outer-write-call-argument", `function take(f: (i32) => i32): i32 { return f(0); } function main(): i32 { let i: i32 = 0; let fs: ((i32) => i32)[] = []; fs = fs.append(((x: i32) => x + i)); i = 4; return take(fs[0]); }`},
		{"outer-write-struct-field", `struct C { f: (i32) => i32, n: i32 } function main(): i32 { let cs: C[] = []; let i: i32 = 0; while (i < 2) { cs = cs.append(C { f: ((x: i32) => x + i), n: i }); i = i + 1; } return (cs[0].f)(0); }`},

		{"per-iteration-capture-control", `function main(): i32 { let t: i32 = 0; let fs: (() => i32)[] = []; let k: i32 = 0; while (k < 3) { let n: i32 = k; fs = fs.append(() => n); k = k + 1; } for f in fs { t = t + f(); } return t; }`},

		{"outer-write-for-iter", `function main(): i32 { let n: i32 = 1; let total: i32 = 0; for f in [(): i32 => { return n; }] { n = 9; total = total + f(); } return total; }`},
		{"lambda-write-for-iter", `function main(): i32 { let n: i32 = 1; let total: i32 = 0; for f in [(): i32 => { n = 9; return 0; }] { total = f(); } return n; }`},
		{"lambda-write-if-cond", `function id(x: i32): i32 { return x; } function main(): i32 { let n: i32 = 1; if (id(((): i32 => { n = 9; return 1; })()) == 1) { return n; } return 0; }`},
		{"lambda-write-while-cond", `function id(x: i32): i32 { return x; } function main(): i32 { let n: i32 = 1; let i: i32 = 0; while (i < 1 && id(((): i32 => { n = 9; return 1; })()) == 1) { i = i + 1; } return n; }`},
		{"lambda-write-match-scrutinee", `enum W { One(i32) } function main(): i32 { let n: i32 = 1; match (W.One(((): i32 => { n = 9; return 1; })())) { W.One(_) => { return n; }, } return 0; }`},

		{"lambda-write-via-defer", `function main(): i32 { let x: i32 = 0; let f = (): i32 => { defer x = 9; return 0; }; f(); return x; }`},
		{"lambda-write-in-guard", `function main(): i32 { let x: i32 = 0; let f = (): i32 => { match (1) { 1 when ((): boolean => { x = 7; return true; })() => { return 1; }, _ => {}, } return 0; }; f(); return x; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			progPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(progPath, src, 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}

			want := interpExit(t, interpBin, tc.src)

			got := runDriverExit(t, runner, interpDriver, src)
			if got != want {
				t.Errorf("%s: self-host interp exited %d, want %d (native oracle) — mutable scalar captures are by-value again", tc.name, got, want)
			}
		})
	}
}

func runDriverExit(t *testing.T, runner []string, bin string, src []byte) int {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = bytes.NewReader(src)
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatal("driver did not run")
	}
	return cmd.ProcessState.ExitCode()
}
