package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostImportAliasIR pins import-alias support in the self-host compiler:
// `import "std/io_buffered" as io;` then `io.bytes_writer_new()`. The qualifier
// `io` differs from the module's mangle prefix (the path basename `io_buffered`),
// so the bundler must map alias -> prefix when flattening qualified references.
// Before this, the self-host parser dropped the `as` clause entirely, so flatten
// didn't recognise `io` as a module qualifier and `io.bytes_writer_new()`
// mis-lowered (`i32.bytes_writer_new` / `const_func[io]`), dragging the whole
// module to the AST emitter. Now `parse_import` captures the alias and flatten's
// resolve_prefix maps `io` -> `io_buffered`.
//
// Asserts the emitted asm calls the alias-resolved mangled symbol
// `io_buffered__bytes_writer_new` (NOT a bare `bytes_writer_new` or an `io.`-
// qualified miss) and that the compiled program runs to exit 0.
//
// Native only: the file-loading driver reads stdlib modules by host path from
// argv (mirrors TestSelfHostStdTestE2E).
func TestSelfHostImportAliasIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "util.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// Alias `io` != basename `io_buffered` — the case the redundant `as <basename>`
	// form (already working) doesn't exercise.
	src := `import "std/io_buffered" as io;
function main(): i32 {
    var w = io.bytes_writer_new().write_string("ok");
    if (w.len() != 2) { return 1; }
    if (w.into_string() != "ok") { return 2; }
    return 0;
}`
	prog := filepath.Join(dir, "alias_prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}

	if got := strings.TrimSpace(runDriverDecide(t, mmc, prog, stdlibRoot)); got != "ir" {
		t.Fatalf("aliased-import program routed %q, want \"ir\"", got)
	}

	asm, err := exec.Command(mmc, prog, stdlibRoot).Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("self-host compile failed: %v", err)
	}
	if !strings.Contains(string(asm), "io_buffered__bytes_writer_new") {
		t.Fatal("aliased call did not resolve to io_buffered__bytes_writer_new (alias->prefix mapping missing)")
	}
	bin := buildBin(t, gcc, dir, "alias_prog", string(asm))
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Errorf("aliased-import IR program exited %d, want 0", code)
	}
}

// runDriverDecide runs the asm_load_run driver with -decide and returns its
// stdout ("ir" / "ast").
func runDriverDecide(t *testing.T, mmc, prog, stdlibRoot string) string {
	t.Helper()
	out, err := exec.Command(mmc, prog, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("-decide failed: %v", err)
	}
	return string(out)
}
