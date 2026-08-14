package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// TestSelfHostEnvIR pins `env(name)` lowering on the self-host x86-64 IR path.
// env looks up an environment variable and returns Option[string] (Some(value)
// when set, None otherwise); it had a full AST runtime (__fern_env) but no IR
// lowering, so a program using it bailed `BAIL call[env]` -> AST, dragging the
// `env_unreachable` test module to the legacy emitter (#3457). It now lowers to
// op_env -> the same __fern_env runtime the AST path calls (x86 transcribed +
// the env-gated _start envp save; arm64 reuses asm_arm64's heap-block runtime).
//
// The program reads FERN_ENV_IR_TEST and exits 0/1/2 for set-matching /
// set-mismatched / unset; the test runs it under all three to exercise the
// Some(value) and None arms plus the value comparison.
func TestSelfHostEnvIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("env IR test runs only natively (sets process env for the child)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (env("FERN_ENV_IR_TEST")) {
        Some(v) => { if (v == "hello") { return 0; } return 1; },
        None => { return 2; },
    }
}`

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_env") {
		t.Fatal("env did not reach the IR runtime path (no __fern_env in asm)")
	}
	progBin := buildBin(t, gcc, dir, "env_prog", string(asm))

	cases := []struct {
		val      string // "" means unset
		set      bool
		wantExit int
	}{
		{"hello", true, 0},
		{"nope", true, 1},
		{"", false, 2},
	}
	for _, tc := range cases {
		run := exec.Command(progBin)
		run.Env = testenv.Clean()
		if tc.set {
			run.Env = testenv.With("FERN_ENV_IR_TEST=" + tc.val)
		}
		_ = run.Run()
		if code := run.ProcessState.ExitCode(); code != tc.wantExit {
			t.Errorf("env(set=%v,val=%q): exit %d, want %d", tc.set, tc.val, code, tc.wantExit)
		}
	}
}

// TestSelfHostEnvIRWasm is the wasm mirror: env(name) now lowers through the
// wasm IR path, not held off it as a strbuf-class exclusion. wasm_ir emits
// `call $__fern_env`, and wasm_ir_run pulls in the
// preview1 environ_sizes_get / environ_get imports + the $__fern_env body (the
// same env_helpers/env_func runtime the AST path uses) + the heap. wasmtime
// supplies env vars via `--env KEY=VAL`; the three cases exercise the
// Some(value) match, the value comparison, and the None arm.
func TestSelfHostEnvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host env wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	const src = `function main(): i32 {
    match (env("FERN_ENV_IR_TEST")) {
        Some(v) => { if (v == "hello") { return 0; } return 1; },
        None => { return 2; },
    }
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_env")) {
		t.Fatal("env did not reach the wasm IR runtime path (no call $__fern_env in WAT)")
	}
	watFile := filepath.Join(dir, "env_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}

	cases := []struct {
		val      string // "" with set=false means unset
		set      bool
		wantExit int
	}{
		{"hello", true, 0},
		{"nope", true, 1},
		{"", false, 2},
	}
	for _, tc := range cases {
		args := []string{"run"}
		if tc.set {
			args = append(args, "--env", "FERN_ENV_IR_TEST="+tc.val)
		}
		args = append(args, watFile)
		run := exec.Command("wasmtime", args...)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("env(set=%v): wasmtime did not exit normally:\n%s", tc.set, wat)
		}
		if code := run.ProcessState.ExitCode(); code != tc.wantExit {
			t.Errorf("env wasm IR (set=%v,val=%q): exit %d, want %d", tc.set, tc.val, code, tc.wantExit)
		}
	}
}
