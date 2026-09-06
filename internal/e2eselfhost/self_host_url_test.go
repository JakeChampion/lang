package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// urlCases bundle the full std/url module plus a main and check the exit code.
// query_parse exercises Map[string, string[]] with the get → Option[string[]] →
// .len() / .append() path that depends on the Map value-type inference. Exit
// codes cross-checked vs the Go backend.
//
// std/url.fern `import "core/map"` (since #1576), so these need a LOADING
// driver. They used to run on the stdin bundle drivers — asm_run / asm_ir_run —
// which resolve no imports; that worked only because the unresolved import left
// the module IR-ineligible and the AST emitter silently picked it up. #5972
// deleted the AST emitters, so the same bundle became a hard
// "module is not IR-eligible" bail and the lane went red on main. The drivers
// say so themselves: "asm_run has no module loader; UNRESOLVED imports:
// core/map … to judge this program use a loading driver".
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

// urlEntry writes std/url.fern plus `mainBody` to a fresh directory and returns
// the entry path, for a loading driver to resolve `core/map` under the stdlib
// root rather than being handed a bundle on stdin.
func urlEntry(t *testing.T, mainBody string) string {
	t.Helper()
	src, err := os.ReadFile("../../internal/stdlib/std/url.fern")
	if err != nil {
		t.Fatalf("read std/url.fern: %v", err)
	}
	proj := t.TempDir()
	entry := filepath.Join(proj, "main.fern")
	body := append(src, []byte("\nfunction main(): i32 { "+mainBody+" }\n")...)
	if err := os.WriteFile(entry, body, 0o644); err != nil {
		t.Fatalf("write url entry: %v", err)
	}
	return entry
}

// urlStdlibRoot is the root a loading driver resolves `core/…` under.
func urlStdlibRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return root
}

// TestSelfHostUrlX86_64 compiles std/url + a main with the self-hosted
// x86-64 compiler and checks exit codes.
func TestSelfHostUrlX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "driver")
	stdlibRoot := urlStdlibRoot(t)

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, cerr := runX86_64Bin(runner, driverBin, urlEntry(t, tc.main), stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
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
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "driver")
	stdlibRoot := urlStdlibRoot(t)

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, cerr := runX86_64Bin(x86runner, driverBin, urlEntry(t, tc.main), stdlibRoot, "-target", "arm64-linux").Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
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
