package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// writeBareVariantScopeFixture lays out a program whose imported enum shares
// a variant name with a builtin: `kinds` declares `Kind { Other(i32) }`, and
// `lib`, which does not import kinds, matches and constructs the builtin
// IoError's `Other(string, string)` by its bare name. The bundler's
// imported-variant map is built over every loaded module, so a bare `Other`
// in lib must still resolve to the builtin: only a module's own imports are
// visible to it. Returns the entry path; the program exits 0 when both
// modules see their own `Other`.
func writeBareVariantScopeFixture(t *testing.T, dir string) string {
	t.Helper()
	files := map[string]string{
		"kinds.fern": "pub enum Kind { Plain, Other(i32) }\n" +
			"pub function code(k: Kind): i32 {\n" +
			"    match (k) {\n" +
			"        Plain => { return 0; },\n" +
			"        Other(n) => { return n; }\n" +
			"    }\n" +
			"    return -1;\n" +
			"}\n",
		"lib.fern": "pub function describe(e: IoError): string {\n" +
			"    match (e) {\n" +
			"        NotFound(_) => { return \"not found\"; },\n" +
			"        Other(_, msg) => { return msg; },\n" +
			"        _ => { return \"other\"; }\n" +
			"    }\n" +
			"    return \"\";\n" +
			"}\n" +
			"pub function fail(): Result[i32, IoError] {\n" +
			"    return Err(Other(\"what\", \"custom\"));\n" +
			"}\n",
		"entry.fern": "import \"./kinds\";\n" +
			"import \"./lib\";\n" +
			"function main(): i32 {\n" +
			"    if (kinds.code(kinds.Other(7)) != 7) { return 1; }\n" +
			"    match (lib.fail()) {\n" +
			"        Ok(_) => { return 2; },\n" +
			"        Err(e) => {\n" +
			"            if (lib.describe(e) != \"custom\") { return 3; }\n" +
			"        }\n" +
			"    }\n" +
			"    return 0;\n" +
			"}\n",
	}
	proj := filepath.Join(dir, "barevariantproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return filepath.Join(proj, "entry.fern")
}

// TestSelfHostBareVariantScope pins that a bare variant name resolves against
// the modules the referencing module imports, not against every module the
// program loads: an imported enum's `Other` must not capture the builtin
// IoError's `Other` in a module that never imports it.
func TestSelfHostBareVariantScope(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	entry := writeBareVariantScopeFixture(t, t.TempDir())
	asm := runDriverFile(t, runner, driverBin, entry)
	bin := buildBin(t, gcc, filepath.Dir(entry), "bare_variant_prog", string(asm))
	cmd := binCmd(runner, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 (1: the imported enum's Other; 2/3: the builtin IoError's Other)", code)
	}
}
