package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		`opaque struct E { a: i32 } function main(): i32 { let e: E = E { a: 3 }; return e.a; }`, 3},
	{"pub-opaque-struct",
		`pub opaque struct E { a: i32 } function main(): i32 { let e: E = E { a: 5 }; return e.a + 4; }`, 9},
	{"both-modifiers",
		`pub opaque struct E { a: i32 } async function compute(): i32 { return 2; } function main(): i32 { let e: E = E { a: 4 }; return e.a + compute(); }`, 6},
	{"still-identifiers",
		`function f(): i32 { let async: i32 = 3; let opaque: i32 = 4; return async + opaque; } function main(): i32 { return f(); }`, 7},
	{"ident-before-other-decl",
		`function async(): i32 { return 6; } function main(): i32 { return async(); }`, 6},
}

func TestSelfHostDeclModifiersChecker(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)
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

func TestSelfHostDeclModifiersIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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

func TestSelfHostDeclModifiersIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host decl-modifier wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range declModifierCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

func TestSelfHostImportAsyncFunctionChecks(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)
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

func TestSelfHostOpaqueUnderAttributes(t *testing.T) {
	pairs := []struct {
		name          string
		plain, opaque string
	}{
		{"must-consume",
			`@must_consume pub struct E { a: i32 } function take(e: E): i32 { return e.a; } function main(): i32 { let e: E = E { a: 6 }; return take(e); }`,
			`@must_consume pub opaque struct E { a: i32 } function take(e: E): i32 { return e.a; } function main(): i32 { let e: E = E { a: 6 }; return take(e); }`},
		{"derive",
			`trait Default { function default(): Self; } @derive(Default) pub struct Cfg { a: i32 } function main(): i32 { let c: Cfg = Cfg.default(); return c.a + 9; }`,
			`trait Default { function default(): Self; } @derive(Default) pub opaque struct Cfg { a: i32 } function main(): i32 { let c: Cfg = Cfg.default(); return c.a + 9; }`},
	}

	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	checkerBin, checkerRunner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)

	check := func(t *testing.T, src string) string {
		t.Helper()
		_, stderr := runSelfHostChecker(t, checkerBin, checkerRunner, src)
		return stderr
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
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
