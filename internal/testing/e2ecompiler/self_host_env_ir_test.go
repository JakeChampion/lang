package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEnvIR pins `env(name)` lowering on the self-host x86-64 IR path.
// env looks up an environment variable and returns Option[string] (Some(value)
// when set, None otherwise); it lowers to op_env, a call into the __fern_env
// runtime, with the env-gated _start saving envp.
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (env("FERN_ENV_IR_TEST")) {
        Some(v) => { if (v == "hello") { return 0; } return 1; },
        None => { return 2; },
    }
}`

	cmd := exec.Command(driverBin)
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
		run.Env = filterEnv(os.Environ(), "FERN_ENV_IR_TEST")
		if tc.set {
			run.Env = append(run.Env, "FERN_ENV_IR_TEST="+tc.val)
		}
		_ = run.Run()
		if code := run.ProcessState.ExitCode(); code != tc.wantExit {
			t.Errorf("env(set=%v,val=%q): exit %d, want %d", tc.set, tc.val, code, tc.wantExit)
		}
	}
}

// TestSelfHostEnvIRWasm is the wasm mirror: wasm_ir emits `call $__fern_env`,
// and the module pulls in the preview1 environ_sizes_get / environ_get imports
// + the $__fern_env body + the heap. wasmtime supplies env vars via
// `--env KEY=VAL`; the three cases exercise the Some(value) match, the value
// comparison, and the None arm.
func TestSelfHostEnvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host env wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (env("FERN_ENV_IR_TEST")) {
        Some(v) => { if (v == "hello") { return 0; } return 1; },
        None => { return 2; },
    }
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

// filterEnv returns environ with any KEY=... entry for key removed.
func filterEnv(environ []string, key string) []string {
	out := environ[:0:0]
	for _, e := range environ {
		if strings.HasPrefix(e, key+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
