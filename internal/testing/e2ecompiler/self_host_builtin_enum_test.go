package e2ecompiler

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
	{"ioerror-payload", "function classify(e: IoError): i32 { match (e) { NotFound(p) => { return 5; }, Interrupted => { return 1; }, _ => { return 0; } } return 9; } function main(): i32 { let e: IoError = NotFound(\"/x\"); return classify(e); }", 5},
	{"ioerror-unit", "function classify(e: IoError): i32 { match (e) { NotFound(p) => { return 5; }, Interrupted => { return 1; }, _ => { return 0; } } return 9; } function main(): i32 { let e: IoError = Interrupted; return classify(e); }", 1},
	// A payload variant reached with NO IoError-typed context to resolve
	// it against — returned as the function's result, and nested inside
	// an `Err(...)`. The checker read every IoError variant as a unit
	// one, so the constructor typed as a VALUE and its own call was
	// "calling non-function value of type IoError" (#9066). Only
	// `Interrupted` and `Unsupported` are unit variants.
	{"ioerror-payload-returned", "function mk(p: string): IoError { return PermissionDenied(p); } function main(): i32 { match (mk(\"/x\")) { PermissionDenied(q) => { return 7; }, _ => { return 0; } } return 9; }", 7},
	{"ioerror-payload-nested", "function mk(p: string): Result[string, IoError] { return Err(NotFound(p)); } function main(): i32 { match (mk(\"/x\")) { Ok(_) => { return 0; }, Err(e) => { match (e) { NotFound(q) => { return 6; }, _ => { return 1; } } } } return 9; }", 6},
	{"ioerror-two-payloads", "function mk(a: string, b: string): IoError { return Other(a, b); } function main(): i32 { match (mk(\"/x\", \"boom\")) { Other(p, m) => { if (m == \"boom\") { return 4; } return 3; }, _ => { return 0; } } return 9; }", 4},
}

// TestSelfHostBuiltinEnumX86_64 — IoError used without a local decl.
func TestSelfHostBuiltinEnumX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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
	cli := buildSelfHostCLI(t)
	for _, tc := range builtinEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
