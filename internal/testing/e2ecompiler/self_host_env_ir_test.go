package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		vars     []string
		wantExit int
	}{
		{[]string{"FERN_ENV_IR_TEST=hello"}, 0},
		{[]string{"FERN_ENV_IR_TEST=nope"}, 1},
		{nil, 2},
		{[]string{"FERN_ENV_IR_TESTX=hello", "FERN_ENV_IR_TES=hello"}, 2},
		{[]string{"FERN_ENV_IR_TESTX=nope", "FERN_ENV_IR_TEST=hello"}, 0},
		{[]string{"FERN_ENV_IR_TEST="}, 1},
	}
	for _, tc := range cases {
		run := exec.Command(progBin)
		run.Env = filterEnv(filterEnv(filterEnv(os.Environ(), "FERN_ENV_IR_TEST"), "FERN_ENV_IR_TESTX"), "FERN_ENV_IR_TES")
		run.Env = append(run.Env, tc.vars...)
		_ = run.Run()
		if code := run.ProcessState.ExitCode(); code != tc.wantExit {
			t.Errorf("env(%q): exit %d, want %d", tc.vars, code, tc.wantExit)
		}
	}
}

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
		val      string
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
