package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// builtinEnumCases construct and match the auto-injected IoError enum
// WITHOUT a local `enum IoError { … }` declaration — exercising the
// self-host emitter's builtin-enum injection (parser.inject_builtin_enums),
// the analogue of the Go checker's builtinEnumDecls. JsonValue injection
// is covered separately by self_host_json_test.go. Exit codes
// cross-checked vs the Go backend.
var builtinEnumCases = []struct {
	name string
	src  string
	exit int
}{
	{"ioerror-payload", "function classify(e: IoError): i32 { match (e) { NotFound(p) => { return 5; }, Interrupted => { return 1; }, _ => { return 0; } } return 9; } function main(): i32 { var e: IoError = NotFound(\"/x\"); return classify(e); }", 5},
	{"ioerror-unit", "function classify(e: IoError): i32 { match (e) { NotFound(p) => { return 5; }, Interrupted => { return 1; }, _ => { return 0; } } return 9; } function main(): i32 { var e: IoError = Interrupted; return classify(e); }", 1},
}

// TestSelfHostBuiltinEnumX86_64 — IoError used without a local decl.
func TestSelfHostBuiltinEnumX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range builtinEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostBuiltinEnumArm64 — CI-gated arm64 counterpart.
func TestSelfHostBuiltinEnumArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range builtinEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
