package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostSimTasks is the sim's task gate on every target
// (docs/NET-P3-SUSPENSION-PLAN.md §3.8): e2eharness.SimTasksProgram runs
// three handlers' plat.http calls as tasks under sim.run_tasks, one of them
// cancelled while it waits, and the self-host's x86-64, arm64 and wasm
// output must be the same bytes: the handlers finishing in virtual-time
// order and the cancelled one's fetch answering Cancelled. The wasm leg
// composes the module with the preview-1 adapter and runs it under
// wasmtime, as TestSelfHostTaskPortable does.
func TestSelfHostSimTasks(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(e2eharness.SimTasksProgram), 0o644); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
				if len(cli.runner) == 0 {
					cmd = exec.Command(bin)
				} else {
					cmd = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
				}
			case "arm64-linux":
				armgcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, "FERN_STRICT_IR=1"))
				if err != nil {
					t.Fatal(err)
				}
				cmd = runArm64Bin(qemu, buildBinArm64(t, armgcc, filepath.Dir(src), "prog", string(asm)))
			case "wasm32-wasi":
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Skip("wasmtime not on PATH")
				}
				wasmtools, err := exec.LookPath("wasm-tools")
				if err != nil {
					t.Skip("wasm-tools not on PATH")
				}
				adapter := os.Getenv("FERN_WASI_ADAPTER")
				if adapter == "" {
					t.Skip("FERN_WASI_ADAPTER unset")
				}
				// The module imports wasi:io/poll's list-returning `poll`, so
				// the composition needs the world's component metadata: embed
				// cmd/fern/wit's `fern` world, then adapt.
				wat := cli.emit(t, src, target, "FERN_STRICT_IR=1")
				core := filepath.Join(filepath.Dir(wat), "prog.core.wasm")
				if out, err := exec.Command(wasmtools, "parse", wat, "-o", core).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools parse: %v\n%s", err, out)
				}
				embedded := filepath.Join(filepath.Dir(wat), "prog.embedded.wasm")
				if out, err := exec.Command(wasmtools, "component", "embed", filepath.Join(repoRootFromTest(t), "cmd", "fern", "wit"),
					"-w", "fern", core, "-o", embedded).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
				}
				comp := filepath.Join(filepath.Dir(wat), "prog.component.wasm")
				if out, err := exec.Command(wasmtools, "component", "new", embedded,
					"--adapt", "wasi_snapshot_preview1="+adapter, "-o", comp).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools component new: %v\n%s", err, out)
				}
				cmd = exec.Command(wasmtime, "run", comp)
			}
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
			}
			if out.String() != e2eharness.SimTasksWant {
				t.Fatalf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out.String(), e2eharness.SimTasksWant, errb.String())
			}
		})
	}
}
