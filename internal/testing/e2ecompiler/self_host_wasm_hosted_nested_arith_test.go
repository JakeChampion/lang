package e2ecompiler

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmHostedCompilerMatchesNativeOnNestedArith(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the self-host wasm-IR driver twice; skipped in -short")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-hosted compiler differential")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-hosted compiler differential")
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
	copySelfHostDriver(t, dir, "fern.fern", "drivers/wasm_ir_run.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	entry := filepath.Join(dir, "drivers/wasm_ir_run.fern")

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

	nativeDrv := compile(t, "wasm_ir_run.native", "-target", "x86-64-linux")
	wasmDrv := compile(t, "wasm_ir_run.wasm", "-target", "wasm32-wasi", "-emit", "core-module")

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

	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{
			name: "two-nested-initialisers",
			src: "function f(a: i32): i32 { let x: i32 = a + a * a; let y: i32 = a + a * a; return x; }\n" +
				"function main(): i32 { return f(1); }\n",
			want: 2,
		},
		{
			name: "nested-initialisers-both-read",
			src: "function g(a: i32): i32 { let v0: i32 = 1 + a * 1; let v1: i32 = 1 + a * 1; return v0 + v1; }\n" +
				"function main(): i32 { return g(3); }\n",
			want: 8,
		},
		{
			name: "four-deep-initialisers",
			src: "function h(a: i32, b: i32): i32 {\n" +
				"    let p: i32 = a + b * (a + b);\n" +
				"    let q: i32 = a + b * (a + b);\n" +
				"    let r: i32 = b + a * (b + a);\n" +
				"    let s: i32 = b + a * (b + a);\n" +
				"    return p + q + r + s;\n" +
				"}\n" +
				"function main(): i32 { return h(2, 3); }\n",
			want: 60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			natOut, natErr, natCode := run(t, exec.Command(nativeDrv), tc.src)
			wasmOut, wasmErr, wasmCode := run(t,
				exec.Command(wasmtime, "run", "--env", "FERN_STRICT_IR=1", wasmDrv), tc.src)

			if natCode != 0 {
				t.Fatalf("native-hosted driver exited %d, want 0\n%s", natCode, natErr)
			}
			if wasmCode != natCode {
				t.Fatalf("wasm-hosted driver exited %d, native %d — the two builds of the same"+
					" compiler disagree\nwasm stderr: %s", wasmCode, natCode, wasmErr)
			}
			if wasmOut != natOut {
				t.Fatalf("wasm-hosted and native-hosted compilers emitted different WAT\n"+
					"native (%d bytes):\n%s\nwasm (%d bytes):\n%s",
					len(natOut), natOut, len(wasmOut), wasmOut)
			}
			if len(natOut) == 0 {
				t.Fatal("driver emitted no module text")
			}

			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, []byte(natOut), 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			binPath := filepath.Join(dir, tc.name+".wasm")
			if o, err := exec.Command(wasmtools, "parse", watPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools parse: %v\n%s", err, o)
			}
			got := 0
			if err := exec.Command(wasmtime, "run", binPath).Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("run emitted module: %v", err)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Fatalf("emitted module returned %d, want %d", got, tc.want)
			}
		})
	}
}
