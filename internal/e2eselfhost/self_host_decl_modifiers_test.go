package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `async` (before `function`) and `opaque` (before `struct`) are the two
// contextual declaration modifiers the native parser accepts. Before #6631
// the self-host parser recognised neither: the modifier fell through the
// top-level decl dispatch into `parse_stmt`, which read it as a bare
// identifier expression, so `async function f()` type-checked as
// `error[E001]: undefined name "async"` and the declaration itself was
// still compiled — a program native accepts, rejected by the self-host for
// a name the source never used.
//
// `async` is STAMPED onto the FuncDecl and reprinted by `-fmt`; `opaque` is
// DROPPED, because native's E021 opaque-access rule needs per-decl module
// provenance the self-host checker does not carry. Neither mark changes
// codegen — the self-host emits no component-model async surface (#6636) —
// so the contract these tests pin is that each form parses, checks clean,
// and reaches codegen as an ordinary function / struct decl, plus that both
// names stay usable as ordinary identifiers. The `async` bit's own
// round-trip is pinned in printer.fern and the CLI driver test.
var declModifierCases = []struct {
	name     string
	src      string
	expected int
}{
	{"async-function",
		`async function compute(): i32 { return 7; } function main(): i32 { return compute(); }`, 7},
	{"pub-async-function",
		`pub async function compute(): i32 { return 8; } function main(): i32 { return compute(); }`, 8},
	{"opaque-struct",
		`opaque struct E { a: i32 } function main(): i32 { var e: E = E { a: 3 }; return e.a; }`, 3},
	{"pub-opaque-struct",
		`pub opaque struct E { a: i32 } function main(): i32 { var e: E = E { a: 5 }; return e.a + 4; }`, 9},
	// Both declarations in one module, each still reachable.
	{"both-modifiers",
		`pub opaque struct E { a: i32 } async function compute(): i32 { return 2; } function main(): i32 { var e: E = E { a: 4 }; return e.a + compute(); }`, 6},
	// Contextual: neither name is reserved, so both stay usable as locals.
	{"still-identifiers",
		`function f(): i32 { var async: i32 = 3; var opaque: i32 = 4; return async + opaque; } function main(): i32 { return f(); }`, 7},
	// `async` / `opaque` NOT followed by their keyword are ordinary
	// identifiers at statement position too — the modifier probe must not
	// swallow them.
	{"ident-before-other-decl",
		`function async(): i32 { return 6; } function main(): i32 { return async(); }`, 6},
}

// TestSelfHostDeclModifiersChecker runs each form through the self-hosted
// type checker (checker_run): the pre-#6631 failure was a spurious E001, so
// a clean exit with empty stderr is the assertion that matters.
func TestSelfHostDeclModifiersChecker(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)
	for _, tc := range declModifierCases {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src)
			if code != 0 {
				t.Fatalf("checker exited %d, want 0\nstderr: %s", code, stderr)
			}
			if diag := strings.TrimSpace(stderr); diag != "" {
				t.Errorf("checker reported %q, want no diagnostic", diag)
			}
		})
	}
}

// TestSelfHostDeclModifiersIRX86_64 routes each form through the self-hosted
// x86-64 driver (asm_run → emit_module, IR default-on), asserting the exit
// code, and probes the routing (asm_pathprobe_run) to pin each case to the
// "ir" path.
func TestSelfHostDeclModifiersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range declModifierCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostDeclModifiersIRWasm runs the same forms through the wasm IR
// backend (wasm_ir_run -ir), so the stack-machine backend is covered too.
func TestSelfHostDeclModifiersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host decl-modifier wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range declModifierCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("decl-modifier wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostImportAsyncFunctionChecks covers `@import(iface, wit) async
// function …` — the Preview-3 async import, which reaches the decl dispatch
// through the attribute arm rather than the top-level probe.
//
// It also pins the body-less exemption from E052. An `@import` declaration
// legitimately has no body, and the self-host checker used to run the
// missing-return walk over it anyway, so EVERY body-less import — async or
// not — drew a spurious "can fall off the end" diagnostic that native does
// not report. The empty block in `function f(): i32 { }` is the same empty
// array to the self-host, so the import attribute is what separates the two;
// the last case pins that the real E052 still fires.
func TestSelfHostImportAsyncFunctionChecks(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)
	const importDecl = `@import("wasi:random/random@0.2.0", "get-random-u64")`
	cases := []struct {
		name, src string
		wantDiag  string
	}{
		{"async-import",
			importDecl + "\nasync function get_random_u64(): i64;\nfunction main(): i32 { return 0; }", ""},
		{"plain-import",
			importDecl + "\nfunction get_random_u64(): i64;\nfunction main(): i32 { return 0; }", ""},
		{"pub-async-import",
			importDecl + "\npub async function get_random_u64(): i64;\nfunction main(): i32 { return 0; }", ""},
		// Not an import: an empty body still falls off the end.
		{"empty-body-still-e052",
			"function f(): i32 { }\nfunction main(): i32 { return 0; }", "error[E052]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src)
			if tc.wantDiag == "" {
				if code != 0 {
					t.Fatalf("checker exited %d, want 0\nstderr: %s", code, stderr)
				}
				if diag := strings.TrimSpace(stderr); diag != "" {
					t.Errorf("checker reported %q, want no diagnostic", diag)
				}
				return
			}
			if got := diagCodes(stderr); got != tc.wantDiag {
				t.Errorf("checker codes = %q, want %q\nstderr: %s", got, tc.wantDiag, stderr)
			}
		})
	}
}

// TestSelfHostOpaqueUnderAttributes covers `opaque` in the two decl arms
// that sit behind an attribute (`@must_consume`, `@derive`), where the
// modifier has to be skipped a second time after the attribute has already
// consumed `pub`.
//
// These arms are asserted by EQUIVALENCE rather than by exit code: both
// attributes carry checker rules of their own whose self-host coverage is
// independently incomplete, and the property #6631 is about is that adding
// `opaque` changes nothing — same diagnostics, byte-identical assembly.
func TestSelfHostOpaqueUnderAttributes(t *testing.T) {
	pairs := []struct {
		name          string
		plain, opaque string
	}{
		{"must-consume",
			`@must_consume pub struct E { a: i32 } function take(e: E): i32 { return e.a; } function main(): i32 { var e: E = E { a: 6 }; return take(e); }`,
			`@must_consume pub opaque struct E { a: i32 } function take(e: E): i32 { return e.a; } function main(): i32 { var e: E = E { a: 6 }; return take(e); }`},
		{"derive",
			`trait Default { function default(): Self; } @derive(Default) pub struct Cfg { a: i32 } function main(): i32 { var c: Cfg = Cfg.default(); return c.a + 9; }`,
			`trait Default { function default(): Self; } @derive(Default) pub opaque struct Cfg { a: i32 } function main(): i32 { var c: Cfg = Cfg.default(); return c.a + 9; }`},
	}

	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	checkerBin, checkerRunner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)

	check := func(t *testing.T, src string) string {
		t.Helper()
		_, stderr := runSelfHostChecker(t, checkerBin, checkerRunner, src)
		return stderr
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			// The diagnostics carry source columns, which the extra `opaque `
			// shifts — compare the codes, which is what the arm decides.
			plainDiag, opaqueDiag := diagCodes(check(t, p.plain)), diagCodes(check(t, p.opaque))
			if plainDiag != opaqueDiag {
				t.Errorf("checker codes differ: plain %q, opaque %q", plainDiag, opaqueDiag)
			}
			plainAsm := runCapture(t, gcc, runner, driverBin, []byte(p.plain))
			opaqueAsm := runCapture(t, gcc, runner, driverBin, []byte(p.opaque))
			if len(plainAsm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes for the plain form")
			}
			if !bytes.Equal(plainAsm, opaqueAsm) {
				t.Errorf("`opaque` changed the emitted assembly (%d vs %d bytes)", len(plainAsm), len(opaqueAsm))
			}
		})
	}
}

// diagCodes extracts the `error[Ennn]` codes from a formatted diagnostic
// stream, discarding the line/column suffixes that shift when a modifier
// widens the source.
func diagCodes(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "error["); i >= 0 {
			if j := strings.Index(line[i:], "]"); j >= 0 {
				out = append(out, line[i:i+j+1])
			}
		}
	}
	return strings.Join(out, ",")
}
