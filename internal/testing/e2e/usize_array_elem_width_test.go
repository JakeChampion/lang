package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A usize element is pointer-width on every target: the native backends
// strided usize[] at 8 but stored and loaded it at 4, so an element past
// 32 bits came back with a zero high word (#10753). Each case checks a
// round-trip against the local it was built from, so the answer is 0 on
// wasm's 32-bit usize as well as on the 64-bit natives.
func TestUsizeArrayElementsKeepTheirHighWord(t *testing.T) {
	const prelude = `function same(a: usize, b: usize): boolean { return (a as u64) == (b as u64); }
function first(xs: usize[]): usize { return xs[0]; }
function main(): i32 {
    let seed: u64 = 8589934599 as u64;
    let zero: usize = 0 as usize;
    let wide: usize = seed as usize;
    if ((wide as u64) != (seed & ((zero - 1) as u64))) { return 100; }
`
	cases := []struct{ name, body string }{
		{"literal", `    let xs: usize[] = [zero, wide];
    if (same(xs[1], wide) && same(xs[0], zero)) { return 0; }
    return 1;`},
		{"append", `    let xs: usize[] = [];
    xs = xs.append(wide);
    xs = xs.append(zero);
    if (same(first(xs), wide) && same(xs[1], zero)) { return 0; }
    return 1;`},
		{"with", `    let xs: usize[] = [zero, zero];
    xs = xs.with(1, wide);
    if (same(xs[1], wide) && same(xs[0], zero)) { return 0; }
    return 1;`},
		{"for-in", `    let xs: usize[] = [wide, wide];
    let sum: u64 = 0 as u64;
    for w in xs { sum = sum + (w as u64); }
    if (sum == (wide as u64) * (2 as u64)) { return 0; }
    return 1;`},
		{"slice", `    let xs: usize[] = [zero, wide];
    let view: [usize] = xs[1:2];
    if (same(view[0], wide)) { return 0; }
    return 1;`},
		{"nested", `    let grid: usize[][] = [[zero, wide]];
    grid = grid.append([wide]);
    if (same(grid[0][1], wide) && same(grid[1][0], wide)) { return 0; }
    return 1;`},
		{"closure-capture", `    let tag: i32 = 3;
    let f = (): usize => { if (tag == 3) { return wide; } return zero; };
    if (same(f(), wide)) { return 0; }
    return 1;`},
		{"cell", `    let box = cell_new(zero);
    box.set(wide);
    if (same(box.get(), wide)) { return 0; }
    return 1;`},
	}

	fern := buildLangBinForInterp(t)
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	wasmtime, wasmErr := exec.LookPath("wasmtime")
	legs := []struct {
		name, target, backend string
		runnable              bool
		run                   func(bin string) *exec.Cmd
	}{
		{"x86-64", "x86-64-linux", "", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"arm64", "arm64-linux", "", armok, func(bin string) *exec.Cmd { return runX86Bin(arm, bin) }},
		{"wasm", "wasm32-wasi", "", wasmErr == nil, func(bin string) *exec.Cmd { return exec.Command(wasmtime, bin) }},
	}
	for _, c := range cases {
		src := prelude + c.body + "\n}\n"
		t.Run(c.name, func(t *testing.T) {
			if code := runInterpExit(t, src); code != 0 {
				t.Fatalf("interp exited %d, want 0\nsrc:\n%s", code, src)
			}
			path := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, leg := range legs {
				t.Run(leg.name, func(t *testing.T) {
					if !leg.runnable {
						t.Skipf("no way to run %s binaries on this host", leg.target)
					}
					bin := filepath.Join(t.TempDir(), "prog")
					args := []string{"-target", leg.target}
					if leg.backend != "" {
						args = append(args, "-backend", leg.backend)
					}
					args = append(args, "-o", bin, path)
					if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					out, err := leg.run(bin).CombinedOutput()
					var exit *exec.ExitError
					if errors.As(err, &exit) {
						t.Fatalf("exited %d, want 0 (the interpreter's answer)\nout: %s\nsrc:\n%s", exit.ExitCode(), out, src)
					} else if err != nil {
						t.Fatalf("run: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
