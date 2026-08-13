package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTransitiveDepsDifferentialX86_64 is the parity gate for a
// dependency that has dependencies of its own (#6756).
//
// The self-host resolved EVERY import against the entry package's directory,
// so a dependency's own imports were looked up in the entry's manifest, found
// nothing, and were skipped — an unresolvable import is silently dropped, so
// the only symptom was a dangling call at check time. Native resolves each
// import against the importing module's directory, which is what makes a
// package graph deeper than one level work at all.
//
// Every existing self-host modload fixture is one level deep, which is why
// nothing caught it. These are two and three, through each of the manifest
// forms that can carry a nested dependency.
func TestSelfHostTransitiveDepsDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("transitive-dep differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	for _, c := range []struct {
		name  string
		files map[string]string
		// wantExit is the compiled program's exit code, so the test proves the
		// transitive module was LINKED, not merely that checking passed.
		wantExit int
	}{
		// app → helper → textkit, all path deps.
		{"two-levels", map[string]string{
			"fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"main.fern": "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
			"../helper/fern.toml": "[package]\nname = \"helper\"\n" +
				"[dependencies]\ntextkit = { path = \"../textkit\" }\n",
			"../helper/lib.fern":   "import \"textkit\";\npub function twelve(): i32 { return textkit.twelve(); }\n",
			"../textkit/fern.toml": "[package]\nname = \"textkit\"\n",
			"../textkit/lib.fern":  "pub function twelve(): i32 { return 12; }\n",
		}, 12},
		// One level deeper again, so a fix that threads only one hop is not
		// enough.
		{"three-levels", map[string]string{
			"fern.toml":      "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\" }\n",
			"main.fern":      "import \"a\";\nfunction main(): i32 { return a.v(); }\n",
			"../a/fern.toml": "[package]\nname = \"a\"\n[dependencies]\nb = { path = \"../b\" }\n",
			"../a/lib.fern":  "import \"b\";\npub function v(): i32 { return b.v() + 1; }\n",
			"../b/fern.toml": "[package]\nname = \"b\"\n[dependencies]\nc = { path = \"../c\" }\n",
			"../b/lib.fern":  "import \"c\";\npub function v(): i32 { return c.v() + 2; }\n",
			"../c/fern.toml": "[package]\nname = \"c\"\n",
			"../c/lib.fern":  "pub function v(): i32 { return 4; }\n",
		}, 7},
		// A dependency's own SIBLING file: `import "./util"` inside the
		// dependency names a file beside the dependency, not beside the entry.
		{"dep-relative-sibling", map[string]string{
			"fern.toml":           "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"main.fern":           "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
			"../helper/fern.toml": "[package]\nname = \"helper\"\n",
			"../helper/lib.fern":  "import \"./util\";\npub function twelve(): i32 { return util.six() * 2; }\n",
			"../helper/util.fern": "pub function six(): i32 { return 6; }\n",
		}, 12},
		// The nested dependency arrives through a WORKSPACE member rather than
		// a path dep, so the member's own manifest is the one consulted.
		{"through-workspace-member", map[string]string{
			"fern.toml": "[workspace]\nmembers = [\"app\", \"lexer\"]\n",
			"app/fern.toml": "[package]\nname = \"app\"\n" +
				"[dependencies]\nlexer = { workspace = true }\n",
			"app/main.fern": "import \"lexer\";\nfunction main(): i32 { return lexer.tok(); }\n",
			"lexer/fern.toml": "[package]\nname = \"lexer\"\n" +
				"[dependencies]\next = { path = \"../../ext\" }\n",
			"lexer/lib.fern":   "import \"ext\";\npub function tok(): i32 { return ext.val(); }\n",
			"../ext/fern.toml": "[package]\nname = \"ext\"\n",
			"../ext/lib.fern":  "pub function val(): i32 { return 41; }\n",
		}, 41},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativePkg := filepath.Join(root, "native", "pkg")
			selfPkg := filepath.Join(root, "selfhost", "pkg")
			for _, pkg := range []string{nativePkg, selfPkg} {
				writeResolveProject(t, pkg, c.files)
			}
			// The workspace case puts main.fern under a member directory.
			entry := "main.fern"
			if _, ok := c.files["app/main.fern"]; ok {
				entry = "app/main.fern"
			}

			// Native first: the fixture has to be a program native accepts, or
			// the comparison pins the fixture rather than the loader.
			nativeOut, nativeErr := exec.Command(nativeBin, "-check", filepath.Join(nativePkg, entry)).CombinedOutput()
			if nativeErr != nil {
				t.Fatalf("native -check failed on the fixture: %v\n%s", nativeErr, nativeOut)
			}
			shOut, shErr := exec.Command(driverBin, "-check", filepath.Join(selfPkg, entry)).CombinedOutput()
			if shErr != nil {
				t.Fatalf("self-host -check failed where native passed — a transitive dependency did not load:\n%s", shOut)
			}

			// Checking clean is not enough: an import that resolves to no file
			// is skipped, so the decisive check is that the program links and
			// runs the transitive code.
			progBin := filepath.Join(selfPkg, "prog")
			if out, err := exec.Command(driverBin, "-target", "x86-64-linux", "-o", progBin,
				filepath.Join(selfPkg, entry)).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile failed: %v\n%s", err, out)
			}
			run := exec.Command(progBin)
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != c.wantExit {
				t.Errorf("program exited %d, want %d", got, c.wantExit)
			}
		})
	}
}
