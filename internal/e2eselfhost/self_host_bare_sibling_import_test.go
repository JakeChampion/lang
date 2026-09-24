package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `import "lib"` names the sibling lib.fern, as native resolves it, whether
// or not a stdlib root is passed. Without a root the self-host skipped every
// non-local import, so the module was never loaded and each call into it read
// as E001 — including when the module itself was the one in error.
func TestSelfHostBareSiblingImportWithoutRoot(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	gcc, runner := x86_64Tooling(t)
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	work := t.TempDir()
	write := func(name, src string) string {
		t.Helper()
		p := filepath.Join(work, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	entry := write("main.fern", "import \"lib\";\nfunction main(): i32 { return lib.f(); }\n")
	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := runX86_64Bin(runner, driver)
		cmd.Args = append(cmd.Args, args...)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	write("lib.fern", "pub function f(): i32 { var x: i32 = 3; return x; }\n")
	if out, code := run("-check", entry); code != 0 {
		t.Fatalf("-check: exit %d\n%s", code, out)
	}
	bin := filepath.Join(work, "prog")
	if out, code := run("-o", bin, entry); code != 0 {
		t.Fatalf("-o: exit %d\n%s", code, out)
	}
	prog := runX86_64Bin(runner, bin)
	_ = prog.Run()
	if code := prog.ProcessState.ExitCode(); code != 3 {
		t.Fatalf("program exit %d, want 3", code)
	}

	write("lib.fern", "pub function f(): i32 { var x: i32 = \"s\"; return x; }\n")
	out, code := run("-check", entry)
	if code != 1 || !strings.Contains(out, "error[E003]") || strings.Contains(out, "E001") {
		t.Fatalf("-check of a module in error: exit %d, want 1 with its E003 and no E001\n%s", code, out)
	}
}
