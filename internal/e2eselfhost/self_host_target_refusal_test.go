package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostRunDriverRefusesUnknownTarget pins what the emit drivers do with
// a `-target` they do not recognise (#6635).
//
// They used to do the worst available thing: dispatch by naming the targets
// they know and letting everything else reach the x86-64 branch, so a stale or
// misspelled name came back as a SUCCESSFUL emit for the wrong machine. That is
// how the retired `arm64` spelling survived this rename in one test — the arm64
// case was handed x86-64 text, and the failure surfaced two layers later as
// "unknown mnemonic `movq'" from the aarch64 assembler, which reads as a
// codegen bug rather than a typo.
//
// Both halves are asserted. The permissive one is the one that matters: the
// current spelling must still emit, or this gate would refuse every real call.
func TestSelfHostRunDriverRefusesUnknownTarget(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver refusal check runs natively (exit codes + stderr)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")

	const src = "function main(): i32 { return 7; }\n"

	for _, c := range []struct {
		name   string
		target string
		// wantExit 0 means "emits"; 2 is the refusal.
		wantExit int
	}{
		{"retired-bare-isa", "arm64", 2},
		{"retired-form-suffix", "arm64-asm", 2},
		{"never-a-target", "vax-11", 2},
		// A target this driver has no branch for is refused rather than
		// silently emitted as x86-64: wasm is a different emitter entirely.
		{"other-backend", "wasm32-wasi", 2},
		{"current-arm64", "arm64-linux", 0},
		{"current-x86-64", "x86-64-linux", 0},
		{"current-darwin", "arm64-darwin", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-target", c.target)
			cmd.Stdin = strings.NewReader(src)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("driver did not exit normally")
			}
			if got := cmd.ProcessState.ExitCode(); got != c.wantExit {
				t.Fatalf("-target %s: exit %d, want %d\nstderr: %s", c.target, got, c.wantExit, stderr.String())
			}
			if c.wantExit != 0 {
				if !strings.Contains(stderr.String(), "unknown -target: "+c.target) {
					t.Errorf("-target %s: stderr = %q, want it to name the rejected target", c.target, stderr.String())
				}
				// The refusal must not also emit: a caller that ignores the
				// exit code would otherwise assemble the wrong machine's text.
				if strings.Contains(stdout.String(), ".globl") {
					t.Errorf("-target %s: refused but still emitted %d bytes of assembly", c.target, stdout.Len())
				}
				return
			}
			if !strings.Contains(stdout.String(), ".globl") {
				t.Errorf("-target %s: emitted no assembly (%q)", c.target, stdout.String())
			}
		})
	}
}
