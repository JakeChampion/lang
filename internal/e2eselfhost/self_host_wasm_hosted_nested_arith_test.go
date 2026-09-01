package e2eselfhost

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmHostedCompilerMatchesNativeOnNestedArith is the differential
// #7948 was invisible to: the SAME self-host driver source, compiled twice by
// the SAME self-host CLI with only -target differing, must answer identically.
//
// The bug it pins was a use-after-free in the compiler's own gate passes. The
// snapshot-param consume-rebind routed a struct-ARRAY parameter through
// __field_reclaim_<ElementType>, a helper written against a STRUCT box; on a
// buffer its field offsets land past the end, so it released whatever words
// followed. Both backends emitted the call, but the wasm reclaim body walks
// four field slots where the register backends' body walks one, so only the
// wasm-hosted compiler dereferenced enough garbage to corrupt an AST node — and
// then bailed with "unknown expression" on a program the native build compiled
// fine.
//
// The programs below are the shape that reached it: two or more depth->=2
// binary initialisers with a NON-CONSTANT operand. One such statement, or
// all-constant nesting (which constant-folds to depth <= 1), leaves the freed
// block unrecycled and the corruption unobservable — the reason the neighbour
// whole-compiler test's `leaf.leaf_tag().len() + 7` never saw it.
//
// Both halves are asserted: identical exit code AND byte-identical WAT. Exit
// code alone would pass on two agreeing refusals; the WAT is what says the
// wasm-hosted compiler computed the same module.
func TestSelfHostWasmHostedCompilerMatchesNativeOnNestedArith(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the self-host wasm-IR driver twice; skipped in -short")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-hosted compiler differential")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-hosted compiler differential")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver takes host filesystem paths as argv; runs only natively")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("stdlib root: %v", err)
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern", "wasm_ir_run.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(dir, "wasm_ir_run.fern")

	// Both drivers come out of the same CLI binary, so the only variable is
	// -target. Each emit peaks in the low gigabytes, so it takes a slot in the
	// process-wide build budget the cold driver builds share.
	compile := func(t *testing.T, out string, args ...string) string {
		t.Helper()
		outPath := filepath.Join(dir, out)
		full := append(append([]string{}, args...), "-o", outPath, entry, stdlibRoot)
		var se bytes.Buffer
		berr := withBuildMemoryMB(3500, func() error {
			cmd := exec.Command(cli, full...)
			cmd.Stderr = &se
			return cmd.Run()
		})
		if berr != nil {
			t.Fatalf("self-host CLI %v failed: %v\n%s", args, berr, se.String())
		}
		return outPath
	}

	nativeDrv := compile(t, "wasm_ir_run.native", "-target", "x86-64-linux")
	wasmDrv := compile(t, "wasm_ir_run.wasm", "-target", "wasm32-wasi", "-emit", "core-module")

	// FERN_STRICT_IR turns a lowering bail into a named diagnostic + exit 3
	// rather than a silent whole-module refusal, so a divergence names the
	// function it happened in instead of only moving the exit code.
	run := func(t *testing.T, cmd *exec.Cmd, src string) (string, string, int) {
		t.Helper()
		var so, se bytes.Buffer
		cmd.Stdin = strings.NewReader(src)
		cmd.Stdout, cmd.Stderr = &so, &se
		code := 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("run driver: %v\n%s", err, se.String())
			}
			code = ee.ExitCode()
		}
		return so.String(), se.String(), code
	}

	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{
			// The reduced #7948 repro: two `a + a * a` initialisers, `a` a
			// parameter so nothing folds.
			name: "two-nested-initialisers",
			src: "function f(a: i32): i32 { var x: i32 = a + a * a; var y: i32 = a + a * a; return x; }\n" +
				"function main(): i32 { return f(1); }\n",
			want: 2,
		},
		{
			// Same depth reached through a literal-heavy spelling, and the
			// results are USED, so a wrong answer is a wrong exit code rather
			// than dead code.
			name: "nested-initialisers-both-read",
			src: "function g(a: i32): i32 { var v0: i32 = 1 + a * 1; var v1: i32 = 1 + a * 1; return v0 + v1; }\n" +
				"function main(): i32 { return g(3); }\n",
			want: 8,
		},
		{
			// Deeper and wider: four initialisers at depth 3.
			name: "four-deep-initialisers",
			src: "function h(a: i32, b: i32): i32 {\n" +
				"    var p: i32 = a + b * (a + b);\n" +
				"    var q: i32 = a + b * (a + b);\n" +
				"    var r: i32 = b + a * (b + a);\n" +
				"    var s: i32 = b + a * (b + a);\n" +
				"    return p + q + r + s;\n" +
				"}\n" +
				"function main(): i32 { return h(2, 3); }\n",
			want: 60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			natOut, natErr, natCode := run(t, exec.Command(nativeDrv), tc.src)
			wasmOut, wasmErr, wasmCode := run(t,
				exec.Command(wasmtime, "run", "--env", "FERN_STRICT_IR=1", wasmDrv), tc.src)

			if natCode != 0 {
				t.Fatalf("native-hosted driver exited %d, want 0\n%s", natCode, natErr)
			}
			if wasmCode != natCode {
				t.Fatalf("wasm-hosted driver exited %d, native %d — the two builds of the same"+
					" compiler disagree\nwasm stderr: %s", wasmCode, natCode, wasmErr)
			}
			if wasmOut != natOut {
				t.Fatalf("wasm-hosted and native-hosted compilers emitted different WAT\n"+
					"native (%d bytes):\n%s\nwasm (%d bytes):\n%s",
					len(natOut), natOut, len(wasmOut), wasmOut)
			}
			if len(natOut) == 0 {
				t.Fatal("driver emitted no module text")
			}

			// The agreed-on module has to be a real one: two identically WRONG
			// texts would pass the comparison above and nothing else looks.
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, []byte(natOut), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			binPath := filepath.Join(dir, tc.name+".wasm")
			if o, err := exec.Command(wasmtools, "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools parse: %v\n%s", err, o)
			}
			got := 0
			if err := exec.Command(wasmtime, "run", binPath).Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("run emitted module: %v", err)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Fatalf("emitted module returned %d, want %d", got, tc.want)
			}
		})
	}
}
