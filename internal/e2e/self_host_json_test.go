package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// jsonCases bundle std/json + a main, and check
// the exit code. Exercises json_parse (objects, arrays, nesting),
// json_encode, and the typed extractors. Exit codes cross-checked vs
// the Go backend.
var jsonCases = []struct {
	name string
	main string
	exit int
}{
	{"encode-number", `var v: JsonValue = JNumber("42"); return json_encode(v).len();`, 2},
	{"encode-string", `var v: JsonValue = JString("hi"); return json_encode(v).len();`, 4},
	{"parse-object-ok", `match (json_parse("{\"a\":1}")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"parse-bad", `match (json_parse("{bad")) { Some(v) => { return 1; }, None => { return 9; } }`, 9},
	{"get-i32", `match (json_parse("{\"n\":42}")) { Some(v) => { match (json_get_i32(v, "n")) { Some(x) => { return x; }, None => { return 0; } } }, None => { return 0; } } return 0;`, 42},
	{"parse-array", `match (json_parse("[1,2,3]")) { Some(v) => { return 7; }, None => { return 0; } }`, 7},
	{"nested-object", `match (json_parse("{\"x\":{\"y\":9}}")) { Some(v) => { match (json_get(v, "x")) { Some(inner) => { match (json_get_i32(inner, "y")) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }, None => { return 0; } } return 0;`, 9},
}

func jsonSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/json.fern")
	if err != nil {
		t.Fatalf("read std/json.fern: %v", err)
	}
	// No local `enum JsonValue { … }` declaration: the self-host
	// emitter injects the builtin enum (parser.inject_builtin_enums),
	// the same way the Go checker auto-injects it.
	out := append([]byte{}, src...)
	out = append(out, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	return out
}

// TestSelfHostJsonX86_64 compiles std/json + a main with the
// self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostJsonX86_64(t *testing.T) {
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

	for _, tc := range jsonCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, jsonSource(t, tc.main))
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

// TestSelfHostJsonArm64 — CI-gated arm64 counterpart.
func TestSelfHostJsonArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range jsonCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, jsonSource(t, tc.main))
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
