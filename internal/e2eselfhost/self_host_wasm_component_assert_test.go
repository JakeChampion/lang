package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Assertions need stderr and exit in the same component, even on the
// successful branch. Exercise the assembled component and its failure path.
func TestSelfHostWasmComponentAssert(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	for _, tc := range []struct {
		name, body, out, diagnostic string
		code                        int
	}{
		{"assert-ok", `assert(true); print("after");`, "after\n", "", 0},
		{"assert-fail", `print("before"); assert(false, "overflow"); print("after");`, "before\n", "assertion failed: overflow\n", 1},
		{"exit-ok", `eprint("done"); exit(0); print("after");`, "", "done\n", 0},
		{"exit-error", `print("before"); eprint("failed"); exit(7); print("after");`, "before\n", "failed\n", 1},
		{"writer-exit", `var w = stderr(); w.write("error\n"); exit(1);`, "", "error\n", 1},
	} {
		for _, mode := range []string{"default", "semantic", "bootstrap"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				src := filepath.Join(t.TempDir(), "assert.fern")
				if err := os.WriteFile(src, []byte(fmt.Sprintf("function main(): i32 { %s return 0; }\n", tc.body)), 0o644); err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(t.TempDir(), "assert.wasm")
				compiler := cli
				args := []string{"-target", "wasm32-wasi", "-o", bin, src}
				if mode == "bootstrap" {
					compiler = bootstrap
				} else {
					args = append(args, stdlib)
				}
				cmd := exec.Command(compiler, args...)
				cmd.Env = append(os.Environ(), "FERN_SEM_IR=0", "FERN_STRICT_IR=1")
				if mode == "semantic" {
					cmd.Env = append(cmd.Env, "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1")
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				run := exec.Command(wasmtime, "run", bin)
				var out, diagnostic bytes.Buffer
				run.Stdout, run.Stderr = &out, &diagnostic
				err := run.Run()
				if run.ProcessState == nil {
					t.Fatalf("run: %v", err)
				}
				if out.String() != tc.out || diagnostic.String() != tc.diagnostic || run.ProcessState.ExitCode() != tc.code {
					t.Fatalf("stdout %q, stderr %q, exit %d; want %q, %q, %d", &out, &diagnostic, run.ProcessState.ExitCode(), tc.out, tc.diagnostic, tc.code)
				}
			})
		}
	}
}
