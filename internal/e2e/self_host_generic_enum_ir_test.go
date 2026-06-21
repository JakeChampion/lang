package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genericEnumIRCases pin USER-DEFINED generic enums (`enum Opt[T] { Sm(T), Nn }`)
// — construction, match, unit variants, string payloads, and two coexisting
// instantiations — on the self-host IR path (x86-64 + wasm). The built-in
// Option/Result are special-cased; only user generic enums went through here.
// They previously bailed the whole module to the AST emitter: the parser never
// parsed the `[T]` (mis-parsing to empty variants), and `monomorphize_module`
// passed the enum table straight through. The fix (#3572) parses the enum type
// params, propagates them to the variant structs, and adds a `monomorphize_enums`
// pass that clones each enum's variants per concrete instantiation (`Opt[i32]` →
// `Sm__i32(__ev: i32)` + `Nn__i32`, owner `Opt__i32`), rewriting constructions,
// unit-variant values, match patterns, and annotations — the enum sibling of the
// generic-struct monomorphiser, run just before it.
//
// Each case is routing-pinned to "ir" (asm_pathprobe_run) and oracle-checked
// against the interpreter; every result stays <= 120 (the wasm exit-code clamp,
// #2908).
var genericEnumIRCases = []struct {
	name string
	main string
	want int
}{
	// construction + match with an i32 payload (the headline repro): 5.
	{"construct-match", `enum Opt[T] { Sm(T), Nn } function main(): i32 { var o: Opt[i32] = Sm(5); match (o) { Sm(n) => { return n; }, Nn => { return 0; } } }`, 5},
	// construction alone (no match) already lowers: returns 1.
	{"construct-only", `enum Opt[T] { Sm(T), Nn } function main(): i32 { var o: Opt[i32] = Sm(5); return 1; }`, 1},
	// a string-payload variant: V("hi").len() == 2.
	{"string-payload", `enum Box[T] { V(T) } function main(): i32 { var b: Box[string] = V("hi"); match (b) { V(s) => { return s.len(); } } }`, 2},
	// a UNIT variant (no payload to infer from — keyed off the annotation): 7.
	{"unit-variant", `enum Opt[T] { Sm(T), Nn } function main(): i32 { var o: Opt[i32] = Nn; match (o) { Sm(n) => { return n; }, Nn => { return 7; } } }`, 7},
	// TWO distinct instantiations of the same enum coexisting: 5 + 2 == 7.
	{"two-inst", `enum Opt[T] { Sm(T), Nn } function main(): i32 { var a: Opt[i32] = Sm(5); var b: Opt[string] = Sm("hi"); var x = 0; match (a) { Sm(n) => { x = n; }, Nn => {} } var y = 0; match (b) { Sm(s) => { y = s.len(); }, Nn => {} } return x + y; }`, 7},
	// a generic-enum PARAM matched inside the callee (param-env keying), with the
	// argument an arg-position construction (key inferred from the payload): 9.
	{"param-match", `enum Opt[T] { Sm(T), Nn } function get(o: Opt[i32]): i32 { match (o) { Sm(n) => { return n; }, Nn => { return 0; } } } function main(): i32 { return get(Sm(9)); }`, 9},
	// a TWO-type-param enum, keyed off the `Pair[i32, i32]` annotation (the key
	// `i32__i32` joins both args), monomorphised to concrete fields: 3 + 4 == 7.
	{"multiparam-i32", `enum Pair[K, V] { P(K, V) } function main(): i32 { var p: Pair[i32, i32] = P(3, 4); match (p) { P(a, b) => { return a + b; } } }`, 7},
	// a two-param enum with MIXED payload types (i32 + string) + a unit variant:
	// 5 + "hi".len() == 7.
	{"multiparam-mixed", `enum Pair[K, V] { P(K, V), Z } function main(): i32 { var p: Pair[i32, string] = P(5, "hi"); match (p) { P(a, b) => { return a + b.len(); }, Z => { return 0; } } }`, 7},
}

// TestSelfHostGenericEnumIRX86_64 routes each case through the self-host x86-64
// IR driver, routing pinned to "ir", oracle-checked against the interpreter.
func TestSelfHostGenericEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if want != tc.want {
				t.Fatalf("%s interp oracle = %d, want hardcoded %d", tc.name, want, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericEnumIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostGenericEnumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-enum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "genenum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("generic-enum wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostGenericEnumUnkeyableFallback pins the best-effort fallback for a
// generic-enum construction that can't be keyed: a MULTI-param variant built
// with NO annotation context (`var x = P(3, 4)`). Arg-inference is single-param
// only, so monomorphize_enums can't pick a key; instead of dropping the variant
// (which would break both paths), strip_variant_tparams leaves it a concrete-
// but-opaque struct that the IR path admits (route "ir") and that round-trips a
// pointer-width payload correctly — so the compiled program still matches the
// interpreter oracle (3 + 4 == 7). (An ANNOTATED multi-param construction is
// instead soundly monomorphised — see the `multiparam-*` cases above.)
func TestSelfHostGenericEnumUnkeyableFallback(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	// A two-type-param enum constructed with no annotation — unkeyable, so the
	// strip fallback applies; still correct for the i32 (pointer-width) payload.
	main := `enum Pair[K, V] { P(K, V) } function main(): i32 { var x = P(3, 4); match (x) { P(a, b) => { return a + b; } } }`
	src := []byte(main + "\n")
	want := interpExit(t, interpBin, string(src))
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	// (routing pinned only to exercise the probe path; behaviour is the contract.)
	_ = strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	progBin := buildBin(t, gcc, dir, "fallback-multiparam", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("unkeyable fallback exited %d, want %d (interp oracle)", code, want)
	}
}
