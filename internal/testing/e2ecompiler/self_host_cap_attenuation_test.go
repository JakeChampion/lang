package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func writeAttenuationProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "app", "main.fern")
}

func attenuationChain(rootGrant, middleGrants string) map[string]string {
	return map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\"" + rootGrant + " }\n",
		"app/main.fern": "import \"a\";\nfunction main(): i32 { return a.one(); }\n",
		"a/fern.toml":   "[package]\nname = \"a\"\n[dependencies]\n" + middleGrants,
		"a/lib.fern":    "import \"b\";\npub function one(): i32 { return b.one(); }\n",
		"b/fern.toml":   "[package]\nname = \"b\"\n",
		"b/lib.fern":    "pub function one(): i32 { return 1; }\n",
	}
}

// The independent Go loader remains the manifest-rule oracle. Every program
// is effect-free: attenuation is a check on granting edges, even when none
// of the granted capabilities is exercised by the program.
func TestSelfHostCapabilityAttenuation(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-attenuation")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, rootGrant, middleGrants string
		wantOK                        bool
	}{
		{"subset", `, capabilities = ["env", "fs"]`, "b = { path = \"../b\", capabilities = [\"fs\"] }\n", true},
		{"amplification", `, capabilities = ["fs"]`, "b = { path = \"../b\", capabilities = [\"env\"] }\n", false},
		{"empty-denies", `, capabilities = []`, "b = { path = \"../b\", capabilities = [\"fs\"] }\n", false},
		{"duplicate-grants", `, capabilities = ["fs", "fs"]`, "b = { path = \"../b\", capabilities = [\"env\", \"env\"] }\n", false},
		{"ungoverned-middle", "", "b = { path = \"../b\", capabilities = [\"env\"] }\n", true},
		{"empty-outgoing", `, capabilities = []`, "b = { path = \"../b\", capabilities = [] }\n", true},
		{"absent-outgoing", `, capabilities = []`, "b = { path = \"../b\" }\n", true},
		{"unused-amplifying-edge", `, capabilities = ["fs"]`, "b = { path = \"../b\", capabilities = [\"fs\"] }\nunused = { path = \"../missing\", capabilities = [\"env\"] }\n", false},
		{"all-violations-sorted", `, capabilities = []`, "z = { path = \"../missing\", capabilities = [\"fs\", \"env\"] }\nb = { path = \"../b\", capabilities = [\"fs\", \"env\"] }\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry := writeAttenuationProject(t, attenuationChain(c.rootGrant, c.middleGrants))
			_, _, oracleErr := modload.Load(entry)
			if (oracleErr == nil) != c.wantOK {
				t.Fatalf("oracle success = %v, want %v: %v", oracleErr == nil, c.wantOK, oracleErr)
			}
			for _, mode := range []string{"check", "compile", "report"} {
				t.Run(mode, func(t *testing.T) {
					args := []string{"-check", entry, stdlib}
					if mode == "compile" {
						args = []string{"-target", "x86-64-linux", "-o", filepath.Join(filepath.Dir(entry), "out"), entry, stdlib}
					} else if mode == "report" {
						args = []string{"-capabilities", entry, stdlib}
					}
					cmd := exec.Command(cli, args...)
					out, runErr := cmd.CombinedOutput()
					if (runErr == nil) != c.wantOK {
						t.Fatalf("success = %v, want %v: %v\n%s", runErr == nil, c.wantOK, runErr, out)
					}
					if oracleErr != nil {
						// Compare every violation and its order, including the
						// granting manifest's path, rather than one substring.
						got := strings.TrimSpace(strings.TrimPrefix(string(out), "fern: "))
						if got != oracleErr.Error() {
							t.Errorf("attenuation diagnostics differ:\ngot:  %s\nwant: %s", got, oracleErr)
						}
					}
				})
			}
		})
	}
}

func TestSelfHostCapabilityAttenuationDiamond(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-attenuation")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprintf("amplifying-sibling-%v", bad), func(t *testing.T) {
			grant := `["env", "fs"]`
			if bad {
				grant = `["env"]`
			}
			files := map[string]string{
				"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na1 = { path = \"../a1\", capabilities = [\"env\", \"fs\"] }\na2 = { path = \"../a2\", capabilities = " + grant + " }\n",
				"app/main.fern": "import \"a1\"; import \"a2\";\nfunction main(): i32 { return a1.one() + a2.two(); }\n",
				"a1/fern.toml":  "[package]\nname = \"a1\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"env\"] }\n",
				"a1/lib.fern":   "import \"b\";\npub function one(): i32 { return b.one(); }\n",
				"a2/fern.toml":  "[package]\nname = \"a2\"\n[dependencies]\nb = { path = \"../b/../b\", capabilities = [\"fs\"] }\n",
				"a2/lib.fern":   "import \"b\";\npub function two(): i32 { return b.one(); }\n",
				"b/fern.toml":   "[package]\nname = \"b\"\n[dependencies]\nc = { path = \"../c\", capabilities = [\"env\", \"fs\"] }\n",
				"b/lib.fern":    "import \"c\";\npub function one(): i32 { return c.one(); }\n",
				"c/fern.toml":   "[package]\nname = \"c\"\n",
				"c/lib.fern":    "pub function one(): i32 { return 1; }\n",
			}
			entry := writeAttenuationProject(t, files)
			_, _, oracleErr := modload.Load(entry)
			if (oracleErr != nil) != bad {
				t.Fatalf("oracle: %v", oracleErr)
			}
			out, runErr := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
			if (runErr != nil) != bad {
				t.Fatalf("success = %v, want %v: %v\n%s", runErr == nil, !bad, runErr, out)
			}
			if bad && !strings.Contains(string(out), oracleErr.Error()) {
				t.Errorf("got %s; want %s", out, oracleErr)
			}
		})
	}
}

func TestSelfHostCapabilityAttenuationDependencyForms(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-attenuation")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range []string{"workspace", "vendor"} {
		for _, bad := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-amplification-%v", form, bad), func(t *testing.T) {
				cap := "fs"
				if bad {
					cap = "env"
				}
				files := attenuationChain(`, capabilities = ["fs"]`, "b = { path = \"../b\", capabilities = [\""+cap+"\"] }\n")
				if form == "workspace" {
					files["fern.toml"] = "[workspace]\nmembers = [\"app\", \"a\", \"b\"]\n"
					files["app/fern.toml"] = strings.ReplaceAll(files["app/fern.toml"], `path = "../a"`, "workspace = true")
					files["a/fern.toml"] = strings.ReplaceAll(files["a/fern.toml"], `path = "../b"`, "workspace = true")
				} else {
					// The vendor tree overrides the declared path on both
					// edges. A grant keyed by the unused path would miss a.
					for name, text := range files {
						if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
							files["app/vendor/"+name] = text
							delete(files, name)
						}
					}
				}
				entry := writeAttenuationProject(t, files)
				_, _, oracleErr := modload.Load(entry)
				if (oracleErr != nil) != bad {
					t.Fatalf("oracle: %v", oracleErr)
				}
				out, runErr := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
				if (runErr != nil) != bad {
					t.Fatalf("success = %v, want %v: %v\n%s", runErr == nil, !bad, runErr, out)
				}
				if bad && !strings.Contains(string(out), oracleErr.Error()) {
					t.Errorf("got %s; want %s", out, oracleErr)
				}
			})
		}
	}
}
