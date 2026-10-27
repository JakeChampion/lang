package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/manifest"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const parityHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var manifestParityCases = []struct{ name, src string }{
	{"minimal", "[package]\nname = \"app\"\n"},
	{"full", "# leading comment\n[package]\nname = \"app\"  # trailing\nversion = \"0.1.0\"\nlib = \"api.fern\"\nindex = \"idx/index.toml\"\n" +
		"[dependencies]\np = { path = \"../p\", capabilities = [\"net\", \"env\", \"net\"] }\nw = { workspace = true }\nv = \"1.2.3\"\n" +
		"t = { version = \"2.0.0\", capabilities = [] }\nu = { url = \"https://example.com/u.tar.gz\", hash = \"" + parityHash + "\" }\n" +
		"[exclude]\nv = [\"1.2.4\", \"1.3.0\"]\n[lint]\ncyclomatic-complexity = \"error\"\n[lint.options]\ncyclomatic-complexity.limit = 15\n"},
	{"workspace only", "[workspace]\nmembers = [\"a\", \"b/c\"]\n"},
	{"empty workspace", "[workspace]\n"},
	{"crlf", "[package]\r\nname = \"app\"\r\n[dependencies]\r\nh = { path = \"h\" }\r\n"},
	{"path with comma and bracket", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"a,b[c\", capabilities = [\"fs\"] }\n"},
	{"empty", ""},

	{"malformed section header", "[package\nname = \"app\"\n"},
	{"unknown section", "[package]\nname = \"app\"\n[features]\n"},
	{"malformed line", "[package]\nname = \"app\"\nedition\n"},
	{"outside a section", "name = \"app\"\n"},
	{"package value unquoted", "[package]\nname = app\n"},
	{"package value escaped", "[package]\nname = \"a\\\\b\"\n"},
	{"unknown package key", "[package]\nname = \"app\"\nedtion = \"x\"\n"},
	{"missing name", "[package]\nversion = \"1.0.0\"\n"},
	{"duplicate section", "[package]\nname = \"app\"\n[dependencies]\n[package]\n"},
	{"duplicate package key", "[package]\nname = \"app\"\nname = \"other\"\n"},
	{"duplicate dependency", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", capabilities = [] }\nh = { path = \"h\" }\n"},
	{"same key in two sections", "[package]\nname = \"app\"\n[dependencies]\nname = { path = \"n\" }\n"},

	{"bare dependency not a string", "[package]\nname = \"app\"\n[dependencies]\nh = 1\n"},
	{"bare dependency not a version", "[package]\nname = \"app\"\n[dependencies]\nh = \"1.2\"\n"},
	{"malformed inline table", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\"\n"},
	{"inline field without =", "[package]\nname = \"app\"\n[dependencies]\nh = { path }\n"},
	{"workspace not true", "[package]\nname = \"app\"\n[dependencies]\nh = { workspace = false }\n"},
	{"capabilities not an array", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", capabilities = \"net\" }\n"},
	{"capability unquoted", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", capabilities = [net] }\n"},
	{"unknown capability", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", capabilities = [\"nett\"] }\n"},
	{"misspelt capabilities key", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", capabilites = [] }\n"},
	{"path unquoted", "[package]\nname = \"app\"\n[dependencies]\nh = { path = h }\n"},
	{"empty path", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"\" }\n"},
	{"url not http", "[package]\nname = \"app\"\n[dependencies]\nh = { url = \"ftp://x/h.tar.gz\", hash = \"" + parityHash + "\" }\n"},
	{"bad hash", "[package]\nname = \"app\"\n[dependencies]\nh = { url = \"https://x/h.tar.gz\", hash = \"sha256:ABC\" }\n"},
	{"bad version", "[package]\nname = \"app\"\n[dependencies]\nh = { version = \"1.x.0\" }\n"},
	{"workspace with path", "[package]\nname = \"app\"\n[dependencies]\nh = { workspace = true, path = \"h\" }\n"},
	{"version with path", "[package]\nname = \"app\"\n[dependencies]\nh = { version = \"1.0.0\", path = \"h\" }\n"},
	{"path and url", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\", url = \"https://x/h.tar.gz\" }\n"},
	{"url without hash", "[package]\nname = \"app\"\n[dependencies]\nh = { url = \"https://x/h.tar.gz\" }\n"},
	{"empty inline table", "[package]\nname = \"app\"\n[dependencies]\nh = {}\n"},
	{"unbalanced bracket", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"h\" ], x = \"y\" }\n"},
	{"invalid dependency name", "[package]\nname = \"app\"\n[dependencies]\n1h = { path = \"h\" }\n"},

	{"unknown workspace key", "[workspace]\nexclude = [\"a\"]\n"},
	{"members not an array", "[workspace]\nmembers = \"a\"\n"},
	{"member unquoted", "[workspace]\nmembers = [a]\n"},

	{"lint value unquoted", "[package]\nname = \"app\"\n[lint]\ncyclomatic-complexity = warn\n"},
	{"lint value empty", "[package]\nname = \"app\"\n[lint]\ncyclomatic-complexity =\n"},
	{"lint option undotted", "[package]\nname = \"app\"\n[lint.options]\nlimit = 3\n"},

	{"invalid exclude name", "[package]\nname = \"app\"\n[exclude]\n9v = [\"1.0.0\"]\n"},
	{"exclude not an array", "[package]\nname = \"app\"\n[exclude]\nv = \"1.0.0\"\n"},
	{"exclude bad version", "[package]\nname = \"app\"\n[exclude]\nv = [\"1.0\"]\n"},
}

func TestSelfHostManifestParseParity(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/manifest_parse_run.fern")
	var paths, want []string
	for i, c := range manifestParityCases {
		p := filepath.Join(dir, fmt.Sprintf("case%02d.toml", i))
		if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		if m, err := manifest.Parse(c.src); err != nil {
			want = append(want, "err "+err.Error())
		} else {
			row := "ok " + strconv.Quote(m.Name) + "|" + strconv.Quote(m.Lib) + "|" + strconv.Quote(m.Index)
			var names []string
			for name := range m.Deps {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				d := m.Deps[name]
				kind := "url"
				switch {
				case d.Workspace:
					kind = "workspace"
				case d.Version != "":
					kind = "version"
				case d.Path != "":
					kind = "path"
				}
				row += "|dep " + strconv.Quote(name) + "|" + kind + "|" + strconv.Quote(d.Path) + "|" + strconv.Quote(d.URL) +
					"|" + strconv.Quote(d.Hash) + "|" + strconv.Quote(d.Version) + "|" + strconv.FormatBool(d.Capabilities != nil) + "|" + strconv.Itoa(d.Line)
				for _, cap := range d.Capabilities {
					row += "|cap " + strconv.Quote(cap)
				}
			}
			want = append(want, row)
		}
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "drivers/manifest_parse_run.fern", "manifest_parse_run", target)
			var cmd *exec.Cmd
			switch target {
			case e2eharness.TargetX86_64Linux:
				cmd = runX86_64Bin(x86_64Runner(t), bin, paths...)
			case e2eharness.TargetArm64Linux:
				cmd = runArm64Bin(arm64Runner(t), bin, paths...)
			default:
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Skip("wasmtime not on PATH")
				}
				var relative []string
				for _, path := range paths {
					relative = append(relative, filepath.Base(path))
				}
				cmd = exec.Command(wasmtime, append([]string{"run", "--dir", dir, bin}, relative...)...)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("manifest_parse_run: %v\n%s", err, out)
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			if len(got) != len(want) {
				t.Fatalf("manifest_parse_run printed %d verdicts for %d manifests:\n%s", len(got), len(want), out)
			}
			for i, c := range manifestParityCases {
				if got[i] != want[i] {
					t.Errorf("%s:\n  self-host: %s\n  Go parser: %s", c.name, got[i], want[i])
				}
			}
		})
	}
}

func TestSelfHostManifestStrictCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("manifest projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-manifest-strict")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, manifest string }{
		{"unknown section", "[package]\nname = \"app\"\n[features]\n[dependencies]\nh = { path = \"../h\", capabilities = [] }\n"},
		{"unknown package key", "[package]\nname = \"app\"\nedtion = \"x\"\n[dependencies]\nh = { path = \"../h\", capabilities = [] }\n"},
		{"misspelt capabilities key", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"../h\", capabilites = [] }\n"},
		{"malformed line", "[package]\nname = \"app\"\n[dependencies]\nh = { path = \"../h\", capabilities = [] }\nh2\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			entry := writeAttenuationProject(t, map[string]string{
				"app/fern.toml": c.manifest,
				"app/main.fern": "import \"h\";\nfunction main(): i32 { return h.one(); }\n",
				"h/fern.toml":   "[package]\nname = \"h\"\n",
				"h/lib.fern":    "pub function one(): i32 { let x = env(\"HOME\"); return 0; }\n",
			})
			_, _, oracleErr := modload.Load(entry)
			if oracleErr == nil {
				t.Fatal("the oracle loader accepted the manifest")
			}
			out, runErr := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
			if runErr == nil {
				t.Fatalf("self-host -check accepted the manifest:\n%s", out)
			}
			if !strings.Contains(string(out), oracleErr.Error()) {
				t.Errorf("self-host -check does not report the oracle's diagnostic\n  self-host: %s\n  oracle:    %s", out, oracleErr)
			}
			if strings.Contains(string(out), "warning:") {
				t.Errorf("self-host -check went on to analyse the program:\n%s", out)
			}
		})
	}
}
