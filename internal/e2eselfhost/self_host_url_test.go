package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// urlCases bundle the full std/url module (it has no imports — it
// relies only on prelude built-ins: Option, structs, string methods,
// and the string-keyed Map runtime) plus a main, and check the exit
// code. query_parse exercises Map[string, string[]] with the get →
// Option[string[]] → .len() / .append() path that depends on the Map
// value-type inference. Exit codes cross-checked vs the Go backend.
var urlCases = []struct {
	name string
	main string
	exit int
}{
	{"parse-some", `match (url_parse("http://example.com/p?q=1")) { Some(u) => { return 1; }, None => { return 0; } }`, 1},
	{"parse-none", `match (url_parse("")) { Some(u) => { return 1; }, None => { return 7; } }`, 7},
	{"query-dup-keys", `var m: Map[string,string[]] = query_parse("a=1&b=2&a=3"); var t: i32 = 0; match (m.get("a")) { Some(v) => { t = t + v.len()*10; }, None => {} } match (m.get("b")) { Some(v) => { t = t + v.len(); }, None => {} } return t;`, 21},
	{"query-has", `var m: Map[string,string[]] = query_parse("x=9"); if (m.has("x") && !m.has("z")) { return 5; } return 0;`, 5},
}

func urlSource(t *testing.T, mainBody string) []byte {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/url.fern")
	if err != nil {
		t.Fatalf("read std/url.fern: %v", err)
	}
	return append(src, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
}

// TestSelfHostUrlX86_64 compiles std/url + a main with the self-hosted
// x86-64 compiler and checks exit codes.
func TestSelfHostUrlX86_64(t *testing.T) {
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

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, urlSource(t, tc.main))
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

// TestSelfHostUrlArm64 — CI-gated arm64 counterpart.
func TestSelfHostUrlArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, urlSource(t, tc.main), "-target", "arm64-linux")
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
