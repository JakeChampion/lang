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

// TestSelfHostWasmUnitsN2 is the first test that exercises MULTI-unit wasm emit
// (#5331 step 4b).
//
// Steps 1-3 and 4a/4b each landed on a byte-identity invariant: whole-program
// output must not change. That is a strong safety property and a weak
// correctness one — it proves nothing was broken, but until this test existed
// nothing ever emitted more than one unit, so "two units link into a working
// module" was unverified.
//
// wasm_units_probe.fern splits a program's functions in half, emits each half as
// its own unit against its own $__str_base/$__fn_base, and links the two through
// wasm.emit_ir_module_units. The assertion is behavioural: the linked two-unit
// module must produce the same answer as the ordinary whole-program build of the
// identical source.
//
// The split is arbitrary rather than along import boundaries, deliberately. An
// arbitrary cut guarantees cross-unit calls in BOTH directions plus literals and
// funcrefs on both sides — strictly harder than the module-boundary case the
// real driver will produce. If this passes, that one is easier.
func TestSelfHostWasmUnitsN2(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm N=2 unit-link e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm N=2 unit-link e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern",
		"wasm_run.fern", "wasm_units_probe.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "wasm_units_probe.fern", "wasm_units_probe")
	wholeBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// Each case must survive an arbitrary bisection of its function list, so the
	// interesting content is spread across several functions: cross-unit calls
	// in both directions, string literals owned by different halves, and a
	// function VALUE taken in one half and called in another.
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "cross_unit_calls_both_directions",
			src: `function a(n: i32): i32 { return b(n) + 1; }
function b(n: i32): i32 { return c(n) * 2; }
function c(n: i32): i32 { return n + 3; }
function d(n: i32): i32 { return a(n) - 1; }
function main(): i32 { return d(4); }`,
			want: 14, // c=7, b=14, a=15, d=14
		},
		{
			name: "literals_owned_by_different_halves",
			src: `function first(): string { return "alpha"; }
function second(): string { return "beta"; }
function third(): string { return "gamma-longer"; }
function fourth(): i32 { return first().len() + second().len(); }
function main(): i32 { return fourth() + third().len(); }`,
			want: 21, // 5 + 4 + 12
		},
		{
			name: "fn_value_taken_in_one_half_called_in_another",
			src: `function add10(n: i32): i32 { return n + 10; }
function twice(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, n: i32): i32 { return f(n); }
function useit(): i32 { return apply(add10, 5) + apply(twice, 3); }
function main(): i32 { return useit(); }`,
			want: 21, // 15 + 6
		},
	}

	// build compiles src with `bin` and returns the program's exit code.
	build := func(t *testing.T, bin, tag, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("%s: driver failed: %v\n%s", tag, err, stderr.String())
		}
		watPath := filepath.Join(dir, tag+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, tag+".wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("%s: wasm-tools parse: %v\n%s", tag, err, out)
		}
		out, err := exec.Command(wasmtime, "run", corePath).CombinedOutput()
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("%s: wasmtime run: %v\n%s", tag, err, out)
			}
			return ee.ExitCode()
		}
		return 0
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The two-unit build is the subject; the whole-program build is the
			// oracle. Comparing against a hardcoded want as well as against the
			// oracle matters — if BOTH paths regressed identically, an
			// oracle-only assertion would stay green.
			whole := build(t, wholeBin, tc.name+"_whole", tc.src)
			if whole != tc.want {
				t.Fatalf("whole-program build returned %d, want %d — the oracle itself is wrong, so the N=2 comparison below would be meaningless", whole, tc.want)
			}
			split := build(t, probeBin, tc.name+"_split", tc.src)
			if split != tc.want {
				t.Errorf("two-unit build returned %d, want %d (whole-program build agrees at %d)", split, tc.want, whole)
			}
		})
	}

	// The probe must actually have emitted two namespaced units. Without this a
	// regression that silently collapsed the split to one unit would leave every
	// assertion above passing while testing nothing.
	t.Run("emits_two_namespaced_units", func(t *testing.T) {
		// Read the LITERALS case specifically: it is the one guaranteed to own
		// string literals in both halves, so its second unit must declare a
		// namespaced string base. Checking a case with neither literals nor
		// function values would assert nothing — both bases are legitimately
		// absent there, since each section is emitted only when non-empty.
		wat, err := os.ReadFile(filepath.Join(dir, "literals_owned_by_different_halves_split.wat"))
		if err != nil {
			t.Fatalf("read split wat: %v", err)
		}
		if !strings.Contains(string(wat), "$__str_base$u1") {
			t.Error("split WAT declares no $__str_base$u1 — the probe emitted a single unit, so the multi-unit path was never exercised")
		}
	})
}
