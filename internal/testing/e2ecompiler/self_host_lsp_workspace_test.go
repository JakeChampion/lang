package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// TestSelfHostLSPWorkspaceX86_64 pins `fern -lsp`'s workspace mode: every open
// document stands in for its file on disk when another imports it, and a
// check publishes what it found in each open module it loaded, at that
// module's own positions.
func TestSelfHostLSPWorkspaceX86_64(t *testing.T) {
	s := buildLSPServers(t)
	const mainSrc = "import \"./util\";\nfunction main(): i32 { return util.thing(); }\n"
	const utilBad = "pub function thing(): i32 { return undeclared_in_util; }\n"
	const utilGood = "pub function thing(): i32 { return 1; }\n"

	run := func(t *testing.T, msgs ...lspMsg) map[string][]lspDiag {
		t.Helper()
		frames, code := runLSP(t, s.selfHost(), session(msgs...))
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		last := map[string][]lspDiag{}
		for _, p := range publishes(t, frames) {
			last[p.URI] = p.Diagnostics
		}
		return last
	}

	t.Run("entry-buffer-overrides-disk", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"main.fern": "function main(): i32 { return undeclared_in_stale; }\n"})
		uri := fileURI(filepath.Join(dir, "main.fern"))
		got := run(t, lspOpen(uri, "function main(): i32 { return 0; }\n"))
		if ds, ok := got[uri]; !ok || len(ds) != 0 {
			t.Errorf("main = %+v (published %v), want an empty publish from the fresh buffer", ds, ok)
		}
	})

	// An imported module is read from its open buffer, not the disk: a fix the
	// editor has not saved clears the entry's view of it, and a break it has
	// not saved shows up.
	t.Run("imported-buffer-overrides-disk", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"main.fern": mainSrc, "util.fern": utilBad})
		mainURI, utilURI := fileURI(filepath.Join(dir, "main.fern")), fileURI(filepath.Join(dir, "util.fern"))
		got := run(t, lspOpen(utilURI, utilGood), lspOpen(mainURI, mainSrc))
		if len(got[mainURI]) != 0 || len(got[utilURI]) != 0 {
			t.Errorf("fixed util buffer: main = %+v, util = %+v, want both clean", got[mainURI], got[utilURI])
		}

		writeTree(t, dir, map[string]string{"util.fern": utilGood})
		got = run(t, lspOpen(utilURI, utilGood), lspOpen(mainURI, mainSrc), lspChange(utilURI, utilBad), lspChange(mainURI, mainSrc+"\n"))
		if len(got[mainURI]) != 0 {
			t.Errorf("broken util buffer: main = %+v, want none of util's findings", got[mainURI])
		}
		if ds := got[utilURI]; len(ds) != 1 || ds[0].Code != "E001" || ds[0].Range.Start.Line != 0 {
			t.Errorf("broken util buffer: util = %+v, want its E001 on line 0", ds)
		}
	})

	// A declaration only the unsaved buffer has is one the entry can use.
	t.Run("entry-sees-what-only-the-buffer-declares", func(t *testing.T) {
		dir := t.TempDir()
		const mainNew = "import \"./util\";\nfunction main(): i32 { return util.thing() + util.added(); }\n"
		writeTree(t, dir, map[string]string{"main.fern": mainNew, "util.fern": utilGood})
		mainURI, utilURI := fileURI(filepath.Join(dir, "main.fern")), fileURI(filepath.Join(dir, "util.fern"))
		got := run(t, lspOpen(utilURI, utilGood+"pub function added(): i32 { return 2; }\n"), lspOpen(mainURI, mainNew))
		if ds, ok := got[mainURI]; !ok || len(ds) != 0 {
			t.Errorf("main = %+v (published %v), want util.added found in util's buffer", ds, ok)
		}
	})

	// A module a manifest dependency resolves to is read from its buffer too:
	// the unsaved edit dropping helper's ungranted fs reach clears the E070.
	t.Run("dependency-package-buffer-overrides-disk", func(t *testing.T) {
		dir := t.TempDir()
		const appMain = "import \"helper\";\nfunction main(): i32 {\n  let r: i32 = helper.save(\"x\");\n  return r;\n}\n"
		const reachesFS = "pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-lsp-e2e.txt\", s);\n  return 0;\n}\n"
		const reachesNothing = "pub function save(s: string): i32 {\n  return s.len();\n}\n"
		writeTree(t, dir, map[string]string{
			"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = [\"net\"] }\n",
			"app/main.fern":    appMain,
			"helper/fern.toml": "[package]\nname = \"helper\"\n",
			"helper/lib.fern":  reachesFS,
		})
		mainURI, libURI := fileURI(filepath.Join(dir, "app", "main.fern")), fileURI(filepath.Join(dir, "helper", "lib.fern"))
		if ds := run(t, lspOpen(mainURI, appMain))[mainURI]; len(ds) != 1 || ds[0].Code != "E070" {
			t.Fatalf("main with helper on disk = %+v, want its E070", ds)
		}
		got := run(t, lspOpen(libURI, reachesNothing), lspOpen(mainURI, appMain))
		if ds, ok := got[mainURI]; !ok || len(ds) != 0 {
			t.Errorf("main = %+v (published %v), want helper read from its buffer", ds, ok)
		}
	})

	// An edit a module's own check cannot see — to a module it imports, which
	// only that module's check loads — reaches it at the next check that
	// loads both.
	t.Run("transitive-change-refreshes-an-open-module", func(t *testing.T) {
		dir := t.TempDir()
		const utilUses = "import \"./helper\";\npub function thing(): i32 { return helper.part(); }\n"
		const helperHas = "pub function part(): i32 { return 1; }\n"
		const helperLacks = "pub function other(): i32 { return 1; }\n"
		writeTree(t, dir, map[string]string{"main.fern": mainSrc, "util.fern": utilUses, "helper.fern": helperHas})
		mainURI := fileURI(filepath.Join(dir, "main.fern"))
		utilURI, helperURI := fileURI(filepath.Join(dir, "util.fern")), fileURI(filepath.Join(dir, "helper.fern"))
		got := run(t, lspOpen(utilURI, utilUses), lspOpen(helperURI, helperHas), lspChange(helperURI, helperLacks), lspOpen(mainURI, mainSrc))
		if ds := got[utilURI]; len(ds) == 0 || ds[0].Range.Start.Line != 1 {
			t.Errorf("util = %+v, want its call to the removed helper.part flagged on line 1", ds)
		}
		if len(got[mainURI]) != 0 {
			t.Errorf("main = %+v, want util's finding kept off main", got[mainURI])
		}
	})

	t.Run("cross-file-diagnostics-route", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"main.fern": mainSrc, "util.fern": utilBad})
		mainURI, utilURI := fileURI(filepath.Join(dir, "main.fern")), fileURI(filepath.Join(dir, "util.fern"))
		got := run(t, lspOpen(utilURI, utilBad), lspOpen(mainURI, mainSrc))
		if len(got[mainURI]) != 0 {
			t.Errorf("main = %+v, want clean", got[mainURI])
		}
		if len(got[utilURI]) == 0 {
			t.Errorf("util lost its diagnostics after main's check")
		}
		// Fixing util through main's check — util's buffer is what main reads —
		// clears util's diagnostics there too.
		got = run(t, lspOpen(utilURI, utilBad), lspOpen(mainURI, mainSrc), lspChange(utilURI, utilGood))
		if ds, ok := got[utilURI]; !ok || len(ds) != 0 {
			t.Errorf("util after the fix = %+v, want an empty publish", ds)
		}
	})

	// A syntax error in an imported module lands on that module, at its line,
	// and nowhere on the entry, which parses fine.
	t.Run("imported-syntax-error", func(t *testing.T) {
		dir := t.TempDir()
		const utilSyntax = "pub function thing(): i32 {\n    return 1 +;\n}\n"
		writeTree(t, dir, map[string]string{"main.fern": mainSrc, "util.fern": utilSyntax})
		mainURI, utilURI := fileURI(filepath.Join(dir, "main.fern")), fileURI(filepath.Join(dir, "util.fern"))
		got := run(t, lspOpen(utilURI, utilSyntax), lspOpen(mainURI, mainSrc))
		if len(got[mainURI]) != 0 {
			t.Errorf("main = %+v, want none of util's syntax errors", got[mainURI])
		}
		if ds := got[utilURI]; len(ds) == 0 || ds[0].Range.Start.Line != 1 {
			t.Errorf("util = %+v, want its syntax error on line 1", ds)
		}
	})

	// A check reassigns only the modules it loaded: an open document outside
	// the entry's imports keeps the diagnostics its own check gave it.
	t.Run("unrelated-document-keeps-its-diagnostics", func(t *testing.T) {
		dir := t.TempDir()
		const other = "function main(): i32 { return undeclared_elsewhere; }\n"
		writeTree(t, dir, map[string]string{"main.fern": mainSrc, "util.fern": utilGood, "other.fern": other})
		mainURI, otherURI := fileURI(filepath.Join(dir, "main.fern")), fileURI(filepath.Join(dir, "other.fern"))
		got := run(t, lspOpen(otherURI, other), lspOpen(mainURI, mainSrc), lspChange(mainURI, mainSrc+"\n"))
		if len(got[otherURI]) != 1 {
			t.Errorf("other = %+v, want its one finding kept", got[otherURI])
		}
	})

	// A multi-file literate document is one entry: a finding in any module it
	// tangles to is the document's, at its document line.
	t.Run("literate-module-findings-land-on-the-document", func(t *testing.T) {
		dir := t.TempDir()
		doc := "# Two modules\n\n```fern file=lib.fern\npub function tag(): i32 { return undeclared_in_lib; }\n```\n\n" +
			"```fern file=main.fern entry\nimport \"./lib\";\nfunction main(): i32 { return lib.tag(); }\n```\n"
		writeTree(t, dir, map[string]string{"prog.fern.md": doc})
		uri := fileURI(filepath.Join(dir, "prog.fern.md"))
		got := run(t, lspOpen(uri, doc))
		if ds := got[uri]; len(ds) != 1 || ds[0].Code != "E001" || ds[0].Range.Start.Line != 3 {
			t.Errorf("document = %+v, want lib's E001 on document line 3", ds)
		}
	})
}
