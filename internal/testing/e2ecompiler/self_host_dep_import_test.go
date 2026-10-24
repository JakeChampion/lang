package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostDepImportDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("dep-import differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	const appDep = "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n"
	workspace := map[string]string{
		"fern.toml":              "[workspace]\nmembers = [\"lexer\", \"handlers/app\"]\n",
		"lexer/fern.toml":        "[package]\nname = \"lexer\"\n",
		"lexer/lib.fern":         "pub function n(): i32 { return 7; }\n",
		"handlers/app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
		"handlers/app/main.fern": "import \"lexer\";\nfunction main(): i32 { return lexer.n(); }\n",
	}
	for _, c := range []struct {
		name       string
		files      map[string]string
		cwd, entry string
		wantExit   int
		checkOnly  bool
	}{
		{"dep-submodule", map[string]string{
			"app/fern.toml":     appDep,
			"app/main.fern":     "import \"helper/extra\";\nfunction main(): i32 { return extra.four(); }\n",
			"helper/fern.toml":  "[package]\nname = \"helper\"\n",
			"helper/extra.fern": "pub function four(): i32 { return 4; }\n",
		}, "app", "main.fern", 4, false},
		{"dep-nested-submodule", map[string]string{
			"app/fern.toml":     appDep,
			"app/main.fern":     "import \"helper/sub/x\";\nfunction main(): i32 { return x.v(); }\n",
			"helper/fern.toml":  "[package]\nname = \"helper\"\n",
			"helper/sub/x.fern": "import \"./y\";\npub function v(): i32 { return y.w() + 1; }\n",
			"helper/sub/y.fern": "pub function w(): i32 { return 8; }\n",
			"helper/y.fern":     "pub function w(): i32 { return 100; }\n",
		}, "app", "main.fern", 9, false},
		{"dep-submodule-named-like-dep", map[string]string{
			"app/fern.toml":      appDep,
			"app/main.fern":      "import \"helper/helper\";\nfunction main(): i32 { return helper.h(); }\n",
			"helper/fern.toml":   "[package]\nname = \"helper\"\n",
			"helper/lib.fern":    "pub function h(): i32 { return 1; }\n",
			"helper/helper.fern": "pub function h(): i32 { return 2; }\n",
		}, "app", "main.fern", 2, false},
		{"dep-lib-in-subdir", map[string]string{
			"app/fern.toml":        appDep,
			"app/main.fern":        "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
			"helper/fern.toml":     "[package]\nname = \"helper\"\nlib = \"src/lib.fern\"\n",
			"helper/src/lib.fern":  "import \"./util\";\npub function twelve(): i32 { return util.two() * 6; }\n",
			"helper/src/util.fern": "pub function two(): i32 { return 2; }\n",
		}, "app", "main.fern", 12, false},
		{"workspace-entry-from-root", workspace, ".", "handlers/app/main.fern", 7, false},
		{"workspace-entry-from-member", workspace, "handlers/app", "main.fern", 7, false},
		{"workspace-root-vendor-from-member", map[string]string{
			"fern.toml":            "[workspace]\nmembers = [\"app\"]\n",
			"app/fern.toml":        "[package]\nname = \"app\"\n[dependencies]\next = { path = \"../../ext\" }\n",
			"app/main.fern":        "import \"ext\";\nfunction main(): i32 { return ext.val(); }\n",
			"vendor/ext/fern.toml": "[package]\nname = \"ext\"\n",
			"vendor/ext/lib.fern":  "pub function val(): i32 { return 42; }\n",
			"../ext/fern.toml":     "[package]\nname = \"ext\"\n",
			"../ext/lib.fern":      "pub function val(): i32 { return 3; }\n",
		}, "app", "main.fern", 42, false},
		{"check-relative-dir", workspace, ".", "handlers", 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativeRoot := filepath.Join(root, "native")
			selfRoot := filepath.Join(root, "selfhost")
			writeResolveProject(t, nativeRoot, c.files)
			writeResolveProject(t, selfRoot, c.files)

			native := exec.Command(nativeBin, "-check", c.entry)
			native.Dir = filepath.Join(nativeRoot, filepath.FromSlash(c.cwd))
			if out, err := native.CombinedOutput(); err != nil {
				t.Fatalf("native -check failed on the fixture: %v\n%s", err, out)
			}

			if c.checkOnly {
				check := exec.Command(driverBin, "-check", c.entry)
				check.Dir = filepath.Join(selfRoot, filepath.FromSlash(c.cwd))
				if out, err := check.CombinedOutput(); err != nil {
					t.Fatalf("self-host -check failed where native passed: %v\n%s", err, out)
				}
				return
			}
			progBin := filepath.Join(root, "prog")
			build := exec.Command(driverBin, "-target", "x86-64-linux", "-o", progBin, c.entry)
			build.Dir = filepath.Join(selfRoot, filepath.FromSlash(c.cwd))
			if out, err := build.CombinedOutput(); err != nil {
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
