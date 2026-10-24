package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostModloadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")

	builtinsSrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}

	helperSrc := "pub function add(x: i32, y: i32): i32 { return x + y; }\n"

	ioErrMain := "" +
		"import \"./helper\";\n" +
		"function classify(e: IoError): i32 {\n" +
		"    match (e) {\n" +
		"        NotFound(m) => { return 1; },\n" +
		"        Other(p, m, _) => { return 2; },\n" +
		"        _ => { return 0; },\n" +
		"    }\n" +
		"}\n" +
		"function main(): i32 {\n" +
		"    let e: IoError = NotFound(\"x\");\n" +
		"    return helper.add(classify(e), 41);\n" +
		"}\n"

	cases := []struct {
		name     string
		files    map[string]string
		entryRel string
		lockDeps map[string]string
		cacheDir bool
		wantExit int
	}{
		{
			name: "real-builtins",
			files: map[string]string{
				"helper.fern":   helperSrc,
				"builtins.fern": string(builtinsSrc),
				"main.fern":     ioErrMain,
			},
			wantExit: 42,
		},
		{
			name: "no-builtins",
			files: map[string]string{
				"helper.fern": helperSrc,
				"main.fern":   ioErrMain,
			},
			wantExit: 42,
		},
		{
			name: "std-subpath",
			files: map[string]string{
				"builtins.fern":  string(builtinsSrc),
				"std/mathx.fern": "pub function triple(x: i32): i32 { return x * 3; }\n",
				"main.fern": "" +
					"import \"std/mathx\";\n" +
					"function main(): i32 { return mathx.triple(14); }\n",
			},
			wantExit: 42,
		},
		{
			name: "manifest-path-dep",
			files: map[string]string{
				"builtins.fern": string(builtinsSrc),
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\ndbl = { path = \"../dbl\" }\n",
				"app/main.fern": "import \"dbl\";\nfunction main(): i32 { return dbl.dbl(21); }\n",
				"dbl/fern.toml": "[package]\nname = \"dbl\"\nlib = \"api.fern\"\n",
				"dbl/api.fern":  "pub function dbl(x: i32): i32 { return x * 2; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
		{
			name: "manifest-dep-lib-in-subdir",
			files: map[string]string{
				"app/fern.toml":        "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
				"app/main.fern":        "import \"helper\";\nfunction main(): i32 { return helper.twelve(); }\n",
				"helper/fern.toml":     "[package]\nname = \"helper\"\nlib = \"src/lib.fern\"\n[dependencies]\ntextkit = { path = \"../textkit\" }\n",
				"helper/src/lib.fern":  "import \"textkit\";\nimport \"./util\";\npub function twelve(): i32 { return textkit.ten() + util.two(); }\n",
				"helper/src/util.fern": "pub function two(): i32 { return 2; }\n",
				"textkit/fern.toml":    "[package]\nname = \"textkit\"\n",
				"textkit/lib.fern":     "pub function ten(): i32 { return 10; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 12,
		},
		{
			name: "manifest-governs-subdirectory-module",
			files: map[string]string{
				"app/fern.toml":  "[package]\nname = \"app\"\n[dependencies]\ndbl = { path = \"../dbl\" }\n",
				"app/main.fern":  "import \"./lib/x\";\nfunction main(): i32 { return x.f(); }\n",
				"app/lib/x.fern": "import \"dbl\";\npub function f(): i32 { return dbl.dbl(21); }\n",
				"dbl/fern.toml":  "[package]\nname = \"dbl\"\n",
				"dbl/lib.fern":   "pub function dbl(x: i32): i32 { return x * 2; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
		{
			name: "workspace-member-dep",
			files: map[string]string{
				"ws/fern.toml":       "[workspace]\nmembers = [\"app\", \"lexer\"]\n",
				"ws/app/fern.toml":   "[package]\nname = \"app\"\n[dependencies]\nlexer = { workspace = true }\n",
				"ws/app/main.fern":   "import \"lexer\";\nfunction main(): i32 { return lexer.tok(); }\n",
				"ws/lexer/fern.toml": "[package]\nname = \"lexer\"\n",
				"ws/lexer/lib.fern":  "pub function tok(): i32 { return 42; }\n",
			},
			entryRel: "ws/app/main.fern",
			wantExit: 42,
		},
		{
			name: "vendored-dep",
			files: map[string]string{
				"app/fern.toml":            "[package]\nname = \"app\"\n[dependencies]\next = { path = \"../ext\" }\n",
				"app/main.fern":            "import \"ext\";\nfunction main(): i32 { return ext.val(); }\n",
				"app/vendor/ext/fern.toml": "[package]\nname = \"ext\"\n",
				"app/vendor/ext/lib.fern":  "pub function val(): i32 { return 42; }\n",
				"ext/fern.toml":            "[package]\nname = \"ext\"\n",
				"ext/lib.fern":             "pub function val(): i32 { return 7; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
		{
			name:     "lock-path-dep",
			lockDeps: map[string]string{"foo": "foo-1.0.0"},
			files: map[string]string{
				"app/fern.toml":       "[package]\nname = \"app\"\nindex = \"index.toml\"\n[dependencies]\nfoo = \"1.0.0\"\n",
				"app/main.fern":       "import \"foo\";\nfunction main(): i32 { return foo.v(); }\n",
				"foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n",
				"foo-1.0.0/lib.fern":  "pub function v(): i32 { return 42; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
		{
			name:     "url-store-dep",
			cacheDir: true,
			files: map[string]string{
				"builtins.fern": string(builtinsSrc),
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\npkg = { url = \"https://example.com/pkg-1.0.0.tar.gz\", hash = \"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\" }\n",
				"app/main.fern": "import \"pkg\";\nfunction main(): i32 { return pkg.v(); }\n",
				"cache/pkgs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/fern.toml": "[package]\nname = \"pkg\"\n",
				"cache/pkgs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/lib.fern":  "pub function v(): i32 { return 42; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
		{
			name:     "lock-url-dep",
			cacheDir: true,
			files: map[string]string{
				"builtins.fern": string(builtinsSrc),
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nbar = \"1.0.0\"\n",
				"app/fern.lock": "[[package]]\nname = \"bar\"\nversion = \"1.0.0\"\nurl = \"https://example.com/bar-1.0.0.tar.gz\"\nhash = \"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"\n",
				"app/main.fern": "import \"bar\";\nfunction main(): i32 { return bar.w(); }\n",
				"cache/pkgs/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/fern.toml": "[package]\nname = \"bar\"\n",
				"cache/pkgs/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/lib.fern":  "pub function w(): i32 { return 42; }\n",
			},
			entryRel: "app/main.fern",
			wantExit: 42,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			if tc.cacheDir {
				t.Setenv("FERN_CACHE_DIR", filepath.Join(progDir, "cache"))
			}
			for name, src := range tc.files {
				dst := filepath.Join(progDir, name)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatalf("mkdir for %s: %v", name, err)
				}
				if err := os.WriteFile(dst, []byte(src), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			if len(tc.lockDeps) > 0 {
				lockDir := filepath.Dir(filepath.Join(progDir, filepath.FromSlash(tc.entryRel)))
				var b strings.Builder
				for dep, rel := range tc.lockDeps {
					fmt.Fprintf(&b, "[[package]]\nname = %q\nversion = \"1.0.0\"\npath = %q\n\n",
						dep, filepath.ToSlash(filepath.Join(progDir, filepath.FromSlash(rel))))
				}
				if err := os.WriteFile(filepath.Join(lockDir, "fern.lock"), []byte(b.String()), 0o644); err != nil {
					t.Fatalf("write fern.lock: %v", err)
				}
			}
			entryRel := tc.entryRel
			if entryRel == "" {
				entryRel = "main.fern"
			}
			entry := filepath.Join(progDir, filepath.FromSlash(entryRel))
			progAsm := runDriverFile(t, runner, driverBin, entry)
			if len(progAsm) == 0 {
				t.Fatalf("driver emitted 0 bytes for %s", tc.name)
			}
			progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_, _ = cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("%s: program exited %d, want %d", tc.name, code, tc.wantExit)
			}
		})
	}
}

func TestSelfHostModloadIRProbeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")

	progDir := t.TempDir()
	files := map[string]string{
		"helper.fern": "pub function dbl(n: i32): i32 { return n * 2; }\n",
		"main.fern": "" +
			"import \"./helper\";\n" +
			"@noinline function f(n: i32): i32 { return helper.dbl(n) + 1; }\n" +
			"function main(): i32 { return f(20); }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, entry, "-ir-probe")
	} else {
		args := append(append([]string{}, runner[1:]...), driverBin, entry, "-ir-probe")
		cmd = exec.Command(runner[0], args...)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run -ir-probe driver: %v", err)
	}
	report := string(out)
	for _, want := range []string{"f: ir", "main: ir", "module: IR"} {
		if !strings.Contains(report, want) {
			t.Errorf("import-aware probe report missing %q\n--- report ---\n%s", want, report)
		}
	}
}

func TestSelfHostModloadArgOrderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "driver")

	entry := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(entry, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	run := func(args ...string) (string, string, int) {
		cmd := runX86_64Bin(runner, driverBin, args...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}

	want, stderr, code := run(entry, "-target", "x86-64-linux")
	if code != 0 || !strings.Contains(want, ".globl") {
		t.Fatalf("entry first: exit %d, %d bytes\n%s", code, len(want), stderr)
	}
	got, stderr, code := run("-target", "x86-64-linux", entry)
	if code != 0 || got != want {
		t.Fatalf("flag first: exit %d, same output as entry first: %v\n%s", code, got == want, stderr)
	}
	for _, args := range [][]string{{entry, "-target", "bogus"}, {"-target", "bogus", entry}} {
		out, stderr, code := run(args...)
		if code != 2 || !strings.Contains(stderr, "unknown -target: bogus") || len(out) != 0 {
			t.Errorf("%v: exit %d, %d bytes, stderr %q; want exit 2 naming the target and no output", args, code, len(out), stderr)
		}
	}
}
