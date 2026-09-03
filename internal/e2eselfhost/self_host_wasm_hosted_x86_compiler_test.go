package e2eselfhost

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmHostedX86CompilerMatchesNative pins #8119: the asm_run
// driver — the self-host x86-64 compiler — built to a wasm core module by the
// self-host CLI must compile a program to the same asm the native build of the
// same driver emits.
//
// Before the fix the wasm-hosted driver ran linear memory to 3.8 GB and
// trapped on `function main(): i32 { return 0; }`. emit_module_funcs builds
// its FnSigs by functional update over the whole-program registry it was
// handed, and a base copy hands the new box every array field pointer with no
// retain; wasm's own `__struct_drop_<T>` classifier then deep-freed those
// `string[]` fields ungated where the register backends' shared classifier
// requires the `strfldok:arr:` / `arrbuf:` admission, so the caller's
// `b.strfld_ok_types` was read after its buffer and elements had been freed
// and recycled. The x86-64-hosted build of the same source never had the arm,
// which is why every native leg was green.
//
// The wasm_ir_run twin of this test (self_host_wasm_hosted_nested_arith_test.go)
// does not reach it: that driver never builds a FnSigs by functional update.
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
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("stdlib root: %v", err)
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern", "asm_run.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(dir, "asm_run.fern")

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
		// The issue's reproduction: the smallest program there is.
		{"return-zero", "function main(): i32 { return 0; }\n"},
		// The same failure reached a different size (1.7 GB against 3.8 GB)
		// on a program with one local, so the volume was input-dependent
		// garbage, not a fixed leak — worth a second point.
		{"one-local", "function main(): i32 { var a: i32 = 1; return a; }\n"},
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
