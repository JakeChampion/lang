package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostModuleDriversEnumVisibility(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("module drivers run natively")
	}
	build := t.TempDir()
	copySelfHostDriver(t, build, "drivers/asm_modload_run.fern", "drivers/checker_modload_run.fern")
	verifier := buildSelfHostBin(t, gcc, build, "drivers/asm_modload_run.fern", "verifier")
	checker := buildSelfHostBin(t, gcc, build, "drivers/checker_modload_run.fern", "checker")
	for _, tc := range []struct {
		name, helper, variant, owner string
		manifest, nested, hidden     bool
	}{
		{name: "public_directory", helper: "lib.fern", variant: "Shared", owner: "Open"},
		{name: "package_directory", helper: "lib.fern", variant: "Here", owner: "Local"},
		{name: "public_manifest", helper: "sub/lib.fern", variant: "Shared", owner: "Open", manifest: true},
		{name: "package_manifest", helper: "sub/lib.fern", variant: "Here", owner: "Local", manifest: true},
		{name: "public_nested_manifest", helper: "sub/lib.fern", variant: "Shared", owner: "Open", manifest: true, nested: true},
		{name: "package_nested_manifest", helper: "sub/lib.fern", variant: "Here", owner: "Local", manifest: true, nested: true, hidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, source string) {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.manifest {
				write("fern.toml", "[package]\nname = \"same\"\n")
			}
			if tc.nested {
				write("sub/fern.toml", "[package]\nname = \"same\"\n")
			}
			write(tc.helper, `enum Hidden { Shared(i32), Here(i32) }
pub enum Open { Shared(string) }
pub(package) enum Local { Here(string) }
`)
			write("main.fern", `import "./`+strings.TrimSuffix(tc.helper, ".fern")+`";
function main(): i32 {
  let node: lib.`+tc.owner+` = lib.`+tc.variant+`("ok");
  match (node) { lib.`+tc.variant+`(text) => { return text.len() - 2; } }
  return 0;
}
`)
			entry := filepath.Join(dir, "main.fern")
			out, err := exec.Command(checker, entry).CombinedOutput()
			if tc.hidden {
				if err == nil || !strings.Contains(string(out), "E087") {
					t.Fatalf("checker must reject the nested package: %v\n%s", err, out)
				}
				return
			}
			if err != nil || len(out) != 0 {
				t.Fatalf("checker: %v\n%s", err, out)
			}
			out, err = exec.Command(verifier, entry, "-verifyprovided").CombinedOutput()
			if err != nil || !strings.Contains(string(out), "irverifyprovided: clean") || !strings.Contains(string(out), "irverifystack: modelled") {
				t.Fatalf("verifier: %v\n%s", err, out)
			}
		})
	}
}

func TestSelfHostModuleDriversMethodVisibility(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("module drivers run natively")
	}
	build := t.TempDir()
	copySelfHostDriver(t, build, "drivers/checker_modload_run.fern")
	checker := buildSelfHostBin(t, gcc, build, "drivers/checker_modload_run.fern", "checker")
	const owner = "[package]\nname = \"app\"\n"
	const lib = `pub(package) function (n: i32) helper(): i32 { return n; }
function (n: i32) private(): i32 { return n; }
`
	for _, tc := range []struct {
		name, method, refused string
		manifest, nested      bool
	}{
		{name: "package_manifest", method: "helper", manifest: true},
		{name: "package_nested_manifest", method: "helper", manifest: true, nested: true, refused: `"helper" is declared without ` + "`pub`"},
		{name: "package_directory_fallback", method: "helper", refused: `"helper" is declared without ` + "`pub`"},
		{name: "private_same_package", method: "private", manifest: true, refused: `"private" is declared without ` + "`pub`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, source string) {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.manifest {
				write("fern.toml", owner)
			}
			if tc.nested {
				write("sub/fern.toml", owner)
			}
			write("sub/x.fern", lib)
			write("main.fern", "import \"./sub/x\";\nfunction main(): i32 { return 42."+tc.method+"(); }\n")
			out, err := exec.Command(checker, filepath.Join(dir, "main.fern")).CombinedOutput()
			if tc.refused == "" {
				if err != nil || len(out) != 0 {
					t.Fatalf("checker: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), tc.refused) {
				t.Fatalf("checker must refuse with %q: %v\n%s", tc.refused, err, out)
			}
		})
	}
}
