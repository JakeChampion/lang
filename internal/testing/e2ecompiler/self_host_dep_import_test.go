package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostDepImportDifferentialX86_64 is the parity gate for the import
// forms that reach into a declared dependency: a module below its root
// (`import "helper/extra"`, #11839), and a module that dependency's lib imports
// beside itself. Native must accept each fixture, and the self-host must build
// it and the program must exit with the value the dependency's code returns.
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
	for _, c := range []struct {
		name  string
		files map[string]string
		// cwd is the directory both compilers run in, relative to the fixture
		// root; entry is the entry file as the command line names it.
		cwd, entry string
		wantExit   int
	}{
		// The first segment names the dependency, the rest a module in it.
		{"dep-submodule", map[string]string{
			"app/fern.toml":     appDep,
			"app/main.fern":     "import \"helper/extra\";\nfunction main(): i32 { return extra.four(); }\n",
			"helper/fern.toml":  "[package]\nname = \"helper\"\n",
			"helper/extra.fern": "pub function four(): i32 { return 4; }\n",
		}, "app", "main.fern", 4},
		// Two segments deep, and the submodule's own `./` import resolves
		// beside it rather than at the dependency's root.
		{"dep-nested-submodule", map[string]string{
			"app/fern.toml":     appDep,
			"app/main.fern":     "import \"helper/sub/x\";\nfunction main(): i32 { return x.v(); }\n",
			"helper/fern.toml":  "[package]\nname = \"helper\"\n",
			"helper/sub/x.fern": "import \"./y\";\npub function v(): i32 { return y.w() + 1; }\n",
			"helper/sub/y.fern": "pub function w(): i32 { return 8; }\n",
			"helper/y.fern":     "pub function w(): i32 { return 100; }\n",
		}, "app", "main.fern", 9},
		// A submodule name that is also the dependency's own name.
		{"dep-submodule-named-like-dep", map[string]string{
			"app/fern.toml":      appDep,
			"app/main.fern":      "import \"helper/helper\";\nfunction main(): i32 { return helper.h(); }\n",
			"helper/fern.toml":   "[package]\nname = \"helper\"\n",
			"helper/lib.fern":    "pub function h(): i32 { return 1; }\n",
			"helper/helper.fern": "pub function h(): i32 { return 2; }\n",
		}, "app", "main.fern", 2},
		// A lib below the dependency's root imports its sibling: `./util` is
		// src/util.fern, not util.fern at the package root.
		{"dep-lib-in-subdir", map[string]string{
			"app/fern.toml":        appDep,
			"app/main.fern":        "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
			"helper/fern.toml":     "[package]\nname = \"helper\"\nlib = \"src/lib.fern\"\n",
			"helper/src/lib.fern":  "import \"./util\";\npub function twelve(): i32 { return util.two() * 6; }\n",
			"helper/src/util.fern": "pub function two(): i32 { return 2; }\n",
		}, "app", "main.fern", 12},
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
