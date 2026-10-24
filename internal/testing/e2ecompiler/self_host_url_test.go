package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var urlCases = []struct {
	name string
	main string
	exit int
}{
	{"parse-some", `match (url_parse("http://example.com/p?q=1")) { Some(u) => { return 1; }, None => { return 0; } }`, 1},
	{"parse-none", `match (url_parse("")) { Some(u) => { return 1; }, None => { return 7; } }`, 7},
	{"query-dup-keys", `let m: Map[string,string[]] = query_parse("a=1&b=2&a=3"); let t: i32 = 0; match (m.get("a")) { Some(v) => { t = t + v.len()*10; }, None => {} } match (m.get("b")) { Some(v) => { t = t + v.len(); }, None => {} } return t;`, 21},
	{"query-has", `let m: Map[string,string[]] = query_parse("x=9"); if (m.contains("x") && !m.contains("z")) { return 5; } return 0;`, 5},
}

func urlEntry(t *testing.T, mainBody string) string {
	t.Helper()
	src, err := os.ReadFile("../../stdlib/std/url.fern")
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

func urlStdlibRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return root
}

func TestSelfHostUrlX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")
	stdlibRoot := urlStdlibRoot(t)

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, cerr := runX86_64Bin(runner, driverBin, urlEntry(t, tc.main), stdlibRoot, "-no-treeshake").Output()
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

func TestSelfHostUrlArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "driver")
	stdlibRoot := urlStdlibRoot(t)

	for _, tc := range urlCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, cerr := runX86_64Bin(x86runner, driverBin, urlEntry(t, tc.main), stdlibRoot, "-no-treeshake", "-target", "arm64-linux").Output()
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
