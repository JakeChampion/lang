package e2ecompiler

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmHostedX86CompilerMatchesNative(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the self-host x86-64 driver twice; skipped in -short")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-hosted x86-64 compiler differential")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver takes host filesystem paths as argv; runs only natively")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("stdlib root: %v", err)
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern", "drivers/asm_run.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(dir, "drivers/asm_run.fern")

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

	nativeDrv := compile(t, "asm_run.native", "-target", "x86-64-linux")
	wasmDrv := compile(t, "asm_run.wasm", "-target", "wasm32-wasi", "-emit", "core-module")

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

	for _, tc := range []struct{ name, src string }{
		{"return-zero", "function main(): i32 { return 0; }\n"},
		{"one-local", "function main(): i32 { let a: i32 = 1; return a; }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			natOut, natErr, natCode := run(t, exec.Command(nativeDrv), tc.src)
			wasmOut, wasmErr, wasmCode := run(t, exec.Command(wasmtime, "run", wasmDrv), tc.src)

			if natCode != 0 {
				t.Fatalf("native-hosted driver exited %d, want 0\n%s", natCode, natErr)
			}
			if wasmCode != natCode {
				t.Fatalf("wasm-hosted driver exited %d, native %d — the two builds of the same"+
					" compiler disagree\nwasm stderr: %s", wasmCode, natCode, wasmErr)
			}
			if !strings.Contains(natOut, "__fn_main:") {
				t.Fatalf("native-hosted driver emitted no __fn_main:\n%s", natOut)
			}
			if wasmOut != natOut {
				t.Fatalf("wasm-hosted and native-hosted compilers emitted different asm\n"+
					"native (%d bytes):\n%s\nwasm (%d bytes):\n%s",
					len(natOut), natOut, len(wasmOut), wasmOut)
			}
		})
	}
}
