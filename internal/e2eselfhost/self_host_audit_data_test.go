package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditDataCases isolate string / array / map features and run them
// through the SELF-HOSTED compiler, asserting the exit code. Self-host
// arm of the §A / §B / §C audit (docs/FEATURE-AUDIT.md); the native arm
// is the `audit_strings_arrays_maps` fixture (all four native backends).
//
// `.with` uses the canonical reassignment idiom (`a = a.with(i, v)`) —
// reading the pre-`.with` binding diverges across backends (#2832).
var auditDataCases = []struct {
	name string
	src  string
	exit int
}{
	// strings
	{"string-len", `function main(): i32 { var s: string = "hello"; return s.len(); }`, 5},
	{"string-concat", `function main(): i32 { var s: string = "ab" + "cde"; return s.len(); }`, 5},
	{"string-eq", `function main(): i32 { if ("abc" == "abc" && "abc" != "abd") { return 5; } return 0; }`, 5},
	{"string-index", `function main(): i32 { var s: string = "ABC"; return (s[0] as i32) + (s[2] as i32); }`, 132},
	{"string-slice", `function main(): i32 { var s: string = "hello"; var t: string = s[1:4] + ""; return t.len(); }`, 3},
	// arrays
	{"array-literal-index", `function main(): i32 { var a: i32[] = [10, 20, 30]; return a[0] + a[2]; }`, 40},
	{"array-len", `function main(): i32 { var a: i32[] = [1, 2, 3, 4]; return a.len(); }`, 4},
	{"array-with", `function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.with(1, 20); return a[0] + a[1] + a[2]; }`, 24},
	{"array-foreach", `function main(): i32 { var a: i32[] = [2, 3, 4]; var s: i32 = 0; for x in a { s = s + x; } return s; }`, 9},
	// maps
	{"map-i32-insert-getor", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,0) + m.get_or(2,0); }`, 119},
	{"map-has", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(5,1); if (m.has(5) && !m.has(9)) { return 7; } return 0; }`, 7},
	{"map-string-keys", `function main(): i32 { var m: Map[string,i32] = map_new(8); m = m.insert("a",3); m = m.insert("b",4); return m.get_or("a",0) + m.get_or("b",0); }`, 7},
	{"map-len", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,1); m = m.insert(2,2); m = m.insert(3,3); return m.len(); }`, 3},
}

// TestSelfHostAuditDataX86_64 runs each string/array/map case through the
// self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditDataX86_64(t *testing.T) {
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

	for _, tc := range auditDataCases {
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

// TestSelfHostAuditDataArm64 — CI-gated arm64 counterpart.
func TestSelfHostAuditDataArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range auditDataCases {
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
