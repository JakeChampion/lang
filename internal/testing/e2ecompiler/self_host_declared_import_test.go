package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostDeclaredImportResolution(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	oracle := buildFernCLIBin(t)
	_, runner, loader := buildCheckerModloadDriverX86(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	const main = "import \"helper\";\nfunction main(): i32 { return helper.answer(); }\n"
	const good = "pub function answer(): i32 { return 42; }\n"
	const bad = "pub function answer(): string { return \"wrong module\"; }\n"
	const locked = "[[package]]\nname = \"helper\"\nversion = \"1.0.0\"\n"
	cases := []struct {
		name, dependency, lock, sibling, source string
		missingLock, lockError                  bool
	}{
		{name: "incomplete-lock", lock: "[[package]]\nname = \"helper\"\n", lockError: true},
		{name: "invalid-version", lock: "[[package]]\nname = \"helper\"\nversion = \"1.x.0\"\n", lockError: true},
		{name: "unknown-lock-field", lock: locked + "unknown = \"x\"\n", lockError: true},
		{name: "invalid-lock-with-sibling", lock: "[[package]]\nname = \"helper\"\n", sibling: good, lockError: true},
		{name: "missing-lock", missingLock: true, lockError: true},
		{name: "missing-lock-entry", lock: "", lockError: true},
		{name: "missing-lock-with-sibling", missingLock: true, sibling: good, lockError: true},
		{name: "missing-lock-source", lock: locked, lockError: true},
		{name: "missing-locked-package", lock: locked + "hash = \"sha256:" + strings.Repeat("a", 64) + "\"\n", lockError: true},
		{name: "declared-path-precedes-sibling", dependency: `{ path = "dep" }`, sibling: bad},
		{name: "declared-subpath-precedes-sibling", dependency: `{ path = "dep" }`, source: "import \"helper/sub\";\nfunction main(): i32 { return sub.answer(); }\n"},
		{name: "explicit-local-precedes-declaration", dependency: `"1.0.0"`, sibling: good, source: "import \"./helper\";\nfunction main(): i32 { return helper.answer(); }\n"},
		{name: "undeclared-sibling", dependency: "none", sibling: good},
		{name: "unused-governed-dependency", dependency: `{ version = "1.0.0", capabilities = ["fs"] }`, lock: "[[package]]\nname = \"helper\"\n", source: "function main(): i32 { return 0; }\n"},
		{name: "used-governed-dependency", dependency: `{ version = "1.0.0", capabilities = ["fs"] }`, lock: "[[package]]\nname = \"helper\"\n", lockError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dependency := tc.dependency
			if dependency == "" {
				dependency = `"1.0.0"`
			}
			manifest := "[package]\nname = \"app\"\n"
			if dependency != "none" {
				manifest += "[dependencies]\nhelper = " + dependency + "\n"
			}
			source := tc.source
			if source == "" {
				source = main
			}
			files := map[string]string{
				"fern.toml":       manifest,
				"main.fern":       source,
				"dep/fern.toml":   "[package]\nname = \"helper\"\n",
				"dep/lib.fern":    good,
				"dep/sub.fern":    good,
				"helper/sub.fern": bad,
			}
			if !tc.missingLock {
				files["fern.lock"] = tc.lock
			}
			if tc.sibling != "" {
				files["helper.fern"] = tc.sibling
			}
			writeResolveProject(t, root, files)
			t.Setenv("FERN_CACHE_DIR", filepath.Join(root, "cache"))
			want := ""
			if tc.lockError {
				_, err := modload.LockedDepDir(root, "helper")
				if err == nil {
					t.Fatal("Go lock lookup accepted an invalid fixture")
				}
				want = err.Error()
			}
			for _, compiler := range []struct {
				name, bin string
				args      []string
			}{
				{"Go", oracle, nil},
				{"self-host", cli, []string{stdlib}},
				{"module-loader", loader, nil},
			} {
				t.Run(compiler.name, func(t *testing.T) {
					args := append([]string{"-check", filepath.Join(root, "main.fern")}, compiler.args...)
					bin := compiler.bin
					if compiler.name == "module-loader" {
						args = []string{filepath.Join(root, "main.fern")}
						if len(runner) != 0 {
							args = append(append(append([]string{}, runner[1:]...), bin), args...)
							bin = runner[0]
						}
					}
					cmd := exec.Command(bin, args...)
					cmd.Dir = root
					cmd.Env = append(os.Environ(), "FERN_CACHE_DIR="+filepath.Join(root, "cache"))
					out, err := cmd.CombinedOutput()
					if want == "" {
						if err != nil {
							t.Fatalf("check: %v\n%s", err, out)
						}
					} else {
						diagnostic := "error[E086]: cannot resolve import \"helper\": " + want
						if compiler.name == "Go" {
							diagnostic = filepath.Join(root, "main.fern") + ": " + want + "\n"
						}
						if err == nil || !strings.Contains(string(out), diagnostic) {
							t.Fatalf("check: %v\n%s\nwant %q", err, out, diagnostic)
						}
					}
				})
			}
		})
	}
}

func TestSelfHostDeclaredModuleTargets(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "modloader.fern")
	const good = "pub function answer(): i32 { return 42; }\n"
	const sibling = "pub function answer(): string { return \"local\"; }\n"
	const invalid = "[[package]]\nname = \"helper\"\n"
	writeResolveProject(t, dir, map[string]string{
		"valid/fern.toml":       "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"dep\" }\n",
		"valid/helper.fern":     sibling,
		"valid/helper/sub.fern": sibling,
		"valid/dep/lib.fern":    good,
		"valid/dep/sub.fern":    good,
		"invalid/fern.toml":     "[package]\nname = \"app\"\n[dependencies]\nhelper = \"1.0.0\"\n",
		"invalid/fern.lock":     invalid,
		"invalid/helper.fern":   good,
		"bare/fern.toml":        "[package]\nname = \"app\"\n",
		"bare/helper.fern":      good,
	})
	_, err := modload.LockedDepDir(filepath.Join(dir, "invalid"), "helper")
	if err == nil {
		t.Fatal("Go lock lookup accepted an incomplete entry")
	}
	cases := []struct{ dir, path, want string }{
		{"valid", "helper", "OK|" + good},
		{"valid", "helper/sub", "OK|" + good},
		{"valid", "./helper", "OK|" + sibling},
		{"bare", "helper", "OK|" + good},
		{"invalid", "helper", "ERR|" + strings.TrimPrefix(err.Error(), dir+string(filepath.Separator)) + "\n"},
	}
	var src strings.Builder
	src.WriteString(`import "./modloader";
function probe(dir: string, path: string): void {
  let r: modloader.Resolved = modloader.resolve_module(modloader.no_overlay(), dir + "/", path);
  if (r.ok) { write("OK|" + r.src); }
  else { print("ERR|" + r.err); }
}
function main(): i32 {
`)
	want := ""
	for _, tc := range cases {
		fmt.Fprintf(&src, "probe(%s, %s);\n", strconv.Quote(tc.dir), strconv.Quote(tc.path))
		want += tc.want
	}
	src.WriteString("return 0;\n}\n")
	writeResolveProject(t, dir, map[string]string{"declared_imports.fern": src.String()})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "declared_imports.fern", "declared_imports", target)
			cmd := mvsDriverCommand(t, target, bin)
			if target == e2eharness.TargetWasm32Wasi {
				cmd.Args = []string{cmd.Path, "run", "--dir=.", bin}
			}
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != want {
				t.Fatalf("module resolution: %v\n%s\nwant:\n%s", err, out, want)
			}
		})
	}
}
