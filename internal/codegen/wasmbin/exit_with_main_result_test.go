package wasmbin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The synthesised `_start` calls main and DROPS its result, so a preview-1 host
// sees exit 0 no matter what the program returned. That is the right shape for
// preview-2 wrapping — `wasi:cli/run` carries only ok/err, which is why
// PrintMainResult exists to smuggle the value out over stdout — but it is wrong
// for a preview-1 command, where proc_exit carries the full byte. The browser
// playground runs exactly that shape: `web/wasi-shim.js` reports "exit 0" for a
// program its own interpreter pane reports as "main() returned exit code 20".
//
// ExitWithMainResult ends the wrapper with proc_exit(main()) instead, the
// wasi-libc `_start` convention.

// runWasmExit runs a preview-1 command module under wasmtime and reports its
// exit code and combined output.
func runWasmExit(t *testing.T, bin []byte) (int, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cmd.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	if wt, err := exec.LookPath("wasm-tools"); err == nil {
		if out, err := exec.Command(wt, "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("module does not validate: %v\n%s", err, out)
		}
	}
	cmd := exec.Command("wasmtime", "run", p)
	out, _ := cmd.CombinedOutput()
	return cmd.ProcessState.ExitCode(), string(out)
}

func TestExitWithMainResultCarriesTheExitCode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	for _, tc := range []struct {
		name     string
		src      string
		wantExit int
		wantOut  string
	}{
		// The headline: a value the wasi:cli/run world cannot carry at all.
		{"a-value-wider-than-ok-err", `function main(): i32 { return 42; }`, 42, ""},
		// 0 must stay 0 — proc_exit(0) is a clean exit, not a failure.
		{"zero-is-success", `function main(): i32 { return 0; }`, 0, ""},
		// 1 is the value the drop path accidentally agreed with, so it is the
		// case that would pass either way; kept as the control.
		{"one", `function main(): i32 { return 1; }`, 1, ""},
		// Output written before the return still reaches the host: proc_exit
		// comes after main returns, not instead of running it.
		{"output-then-exit", `function main(): i32 { print("hi"); return 3; }`, 3, "hi\n"},
		// 125 is the widest status wasmtime's preview-1 proc_exit accepts —
		// it traps on anything outside [0, 126), which an explicit `exit(200)`
		// already hits today, so the wrapper masks nothing the language does
		// not. A browser shim reading `code & 0xff` has no such limit.
		{"the-widest-status-the-host-takes", `function main(): i32 { return 125; }`, 125, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := loadAndCheckModule(t, tc.src)
			bin, err := BuildWithOptions(prog, info, BuildOptions{
				ExitWithMainResult: true,
				ForceMemorySection: true,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			gotExit, gotOut := runWasmExit(t, bin)
			if gotExit != tc.wantExit {
				t.Errorf("exit %d, want %d (output %q)", gotExit, tc.wantExit, gotOut)
			}
			if gotOut != tc.wantOut {
				t.Errorf("output %q, want %q", gotOut, tc.wantOut)
			}
		})
	}
}

// The control: without the option the same program exits 0, which is what makes
// the flag necessary rather than cosmetic. Also pins that preview-2 wrapping —
// the other SynthStart caller — is unaffected by the change.
func TestSynthStartAloneStillDropsMainsResult(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	prog, info := loadAndCheckModule(t, `function main(): i32 { return 42; }`)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		SynthStart:         true,
		ForceMemorySection: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if gotExit, gotOut := runWasmExit(t, bin); gotExit != 0 {
		t.Errorf("exit %d, want 0 — SynthStart on its own must keep dropping main's result (output %q)", gotExit, gotOut)
	}
}

// PrintMainResult and ExitWithMainResult both want main's i32, so the wrapper
// stashes it in a local. Together the value must reach BOTH channels — a
// wrapper that let print consume the only copy would exit 0.
func TestPrintAndExitShareMainsResult(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	prog, info := loadAndCheckModule(t, `
import "core/int";
function main(): i32 { return 42; }
`)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		PrintMainResult:    true,
		ExitWithMainResult: true,
		ForceMemorySection: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	gotExit, gotOut := runWasmExit(t, bin)
	if gotExit != 42 {
		t.Errorf("exit %d, want 42 (output %q)", gotExit, gotOut)
	}
	if !strings.Contains(gotOut, "42") {
		t.Errorf("output %q should contain main's printed value", gotOut)
	}
}

// A void main has no value for the exit code to carry, so it exits 0 rather
// than tripping the wrapper's stack bookkeeping.
func TestVoidMainExitsZero(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	prog, info := loadAndCheckModule(t, `function main(): void { print("done"); }`)
	bin, err := BuildWithOptions(prog, info, BuildOptions{
		ExitWithMainResult: true,
		ForceMemorySection: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	gotExit, gotOut := runWasmExit(t, bin)
	if gotExit != 0 {
		t.Errorf("exit %d, want 0 (output %q)", gotExit, gotOut)
	}
	if gotOut != "done\n" {
		t.Errorf("output %q, want %q", gotOut, "done\n")
	}
}
