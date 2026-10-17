package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/tools/gates"
)

func TestSelfHostCapabilityPackageIdentity(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-cap-identity")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		helperName string
		grant      string
		local      bool
		allowed    bool
	}{
		{"local-module-denied", "helper", "[]", true, false},
		{"local-module-granted", "helper", `["env"]`, true, true},
		{"root-name-denied", "app", "[]", false, false},
		{"root-name-granted", "app", `["env"]`, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = " + c.grant + " }\n",
				"app/main.fern":    "import \"helper\";\nfunction main(): i32 { return helper.one(); }\n",
				"helper/fern.toml": "[package]\nname = \"" + c.helperName + "\"\n",
				"helper/lib.fern":  "pub function one(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\n",
			}
			if c.local {
				files["helper/lib.fern"] = "import \"./sub/inner\";\npub function one(): i32 { return 0; }\n"
				files["helper/sub/inner.fern"] = "pub function latent(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\n"
				// A same-named root module must not steal the dependency's
				// ownership when the bundler disambiguates symbol prefixes.
				files["app/main.fern"] = "import \"./inner\"; import \"helper\";\nfunction main(): i32 { return helper.one(); }\n"
				files["app/inner.fern"] = "pub function root_only(): i32 { return 0; }\n"
			}
			entry := writeAttenuationProject(t, files)
			prog, _, err := modload.Load(entry)
			if err != nil {
				t.Fatal(err)
			}
			_, nativeErr := gates.Capabilities(entry, prog)
			if (nativeErr == nil) != c.allowed {
				t.Errorf("Go gate success = %v, want %v: %v", nativeErr == nil, c.allowed, nativeErr)
			}
			out, runErr := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
			if (runErr == nil) != c.allowed {
				t.Errorf("self-host success = %v, want %v: %v\n%s", runErr == nil, c.allowed, runErr, out)
			}
			if !c.allowed && runErr != nil && !strings.Contains(string(out), "error[E070]") {
				t.Errorf("missing capability refusal: %s", out)
			}
		})
	}
}

func TestSelfHostCapabilityPackageBoundaries(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-cap-boundaries")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		manifest bool
		relative bool
		grant    string
		allowed  bool
	}{
		{"manifestless-denied", false, false, "[]", false},
		{"manifestless-granted", false, false, `["env"]`, true},
		{"relative-denied", true, true, "[]", false},
		{"relative-granted", true, true, `["env"]`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ip := "helper"
			if c.relative {
				ip = "../helper/lib"
			}
			files := map[string]string{
				"app/fern.toml":     "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = " + c.grant + " }\n",
				"app/main.fern":     "import \"" + ip + "\";\nfunction main(): i32 { return 0; }\n",
				"helper/lib.fern":   "import \"./inner\";\npub function one(): i32 { return 0; }\n",
				"helper/inner.fern": "pub function latent(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\n",
			}
			if c.manifest {
				files["helper/fern.toml"] = "[package]\nname = \"helper\"\n"
			}
			entry := writeAttenuationProject(t, files)
			prog, _, err := modload.Load(entry)
			if err != nil {
				t.Fatal(err)
			}
			_, nativeErr := gates.Capabilities(entry, prog)
			if (nativeErr == nil) != c.allowed {
				t.Errorf("Go gate = %v, want allowed %v", nativeErr, c.allowed)
			}
			out, err := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
			if (err == nil) != c.allowed {
				t.Errorf("self-host = %v, want allowed %v: %s", err, c.allowed, out)
			}
		})
	}
	t.Run("root-function-spelling", func(t *testing.T) {
		entry := writeAttenuationProject(t, map[string]string{
			"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = [] }\n",
			"app/main.fern":    "import \"helper\";\nfunction helper__root_only(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\nfunction main(): i32 { return helper.one(); }\n",
			"helper/fern.toml": "[package]\nname = \"helper\"\n",
			"helper/lib.fern":  "pub function one(): i32 { return 0; }\n",
		})
		prog, _, err := modload.Load(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gates.Capabilities(entry, prog); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(cli, "-check", entry, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("root name was misattributed: %v: %s", err, out)
		}
	})
	t.Run("same-name-dependencies", func(t *testing.T) {
		entry := writeAttenuationProject(t, map[string]string{
			"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"env\"] }\nb = { path = \"../b\", capabilities = [] }\n",
			"app/main.fern": "import \"a\"; import \"b\";\nfunction main(): i32 { return 0; }\n",
			"a/fern.toml":   "[package]\nname = \"shared\"\n",
			"b/fern.toml":   "[package]\nname = \"shared\"\n",
			"a/lib.fern":    "pub function one(): i32 { return 0; }\n",
			"b/lib.fern":    "pub function latent(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\n",
		})
		prog, _, err := modload.Load(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gates.Capabilities(entry, prog); err == nil {
			t.Error("same-name dependency borrowed its sibling's grant")
		}
		if out, err := exec.Command(cli, "-check", entry, stdlib).CombinedOutput(); err == nil || !strings.Contains(string(out), "error[E070]") {
			t.Fatalf("same-name dependency accepted: %v: %s", err, out)
		}
		rows, _ := gates.CapabilityRows(entry, prog)
		if len(rows) != 3 {
			t.Errorf("Go report merged identities: %v", rows)
		}
		report, err := exec.Command(cli, "-capabilities", entry, stdlib).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(report), "shared (") != 2 {
			t.Errorf("self-host report merged identities: %s", report)
		}
	})
	t.Run("authorized-union", func(t *testing.T) {
		entry := writeAttenuationProject(t, map[string]string{
			"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [\"env\"] }\nb = { path = \"../b\", capabilities = [\"fs\"] }\n",
			"app/main.fern": "import \"a\"; import \"b\";\nfunction main(): i32 { return 0; }\n",
			"a/fern.toml":   "[package]\nname = \"a\"\n[dependencies]\nc = { path = \"../c\", capabilities = [\"env\"] }\n",
			"b/fern.toml":   "[package]\nname = \"b\"\n[dependencies]\nc = { path = \"../c\", capabilities = [\"fs\"] }\n",
			"a/lib.fern":    "import \"c\"; pub function one(): i32 { return 0; }\n",
			"b/lib.fern":    "import \"c\"; pub function one(): i32 { return 0; }\n",
			"c/fern.toml":   "[package]\nname = \"c\"\n",
			"c/lib.fern":    "pub function latent(): i32 { let x = env(\"FERN_CAP_REPRO\"); let y = read_file(\"unused\"); return 0; }\n",
		})
		prog, _, err := modload.Load(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gates.Capabilities(entry, prog); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(cli, "-check", entry, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("authorized union refused: %v: %s", err, out)
		}
	})
}

func TestSelfHostCapabilityGrantCycles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-cap-cycles")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, rootGrant, shape string
		allowed                bool
	}{
		{"self-unseeded", "[]", "self", false},
		{"self-seeded", `["env"]`, "self", true},
		{"pair-unseeded", "[]", "pair", false},
		{"pair-other-capability", `["fs"]`, "pair", false},
		{"pair-seeded", `["env"]`, "pair", true},
		{"root-back-edge", `["env"]`, "root", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := attenuationChain(", capabilities = "+c.rootGrant, "b = { path = \"../b\", capabilities = [\"env\"] }\n")
			switch c.shape {
			case "self":
				files["a/fern.toml"] = "[package]\nname = \"a\"\n[dependencies]\nloop = { path = \".\", capabilities = [\"env\"] }\n"
				files["a/lib.fern"] = "pub function one(): i32 { return 0; }\n"
			case "pair":
				files["b/fern.toml"] = "[package]\nname = \"b\"\n[dependencies]\nback = { path = \"../a\", capabilities = [\"env\"] }\n"
			case "root":
				files["a/fern.toml"] = "[package]\nname = \"a\"\n[dependencies]\nparent = { path = \"../app\", capabilities = [] }\n"
				files["a/lib.fern"] = "pub function one(): i32 { return 0; }\n"
			}
			entry := writeAttenuationProject(t, files)
			_, _, nativeErr := modload.Load(entry)
			if (nativeErr == nil) != c.allowed {
				t.Errorf("Go loader success = %v, want %v: %v", nativeErr == nil, c.allowed, nativeErr)
			}
			for _, mode := range []string{"-check", "-capabilities"} {
				out, runErr := exec.Command(cli, mode, entry, stdlib).CombinedOutput()
				if (runErr == nil) != c.allowed {
					t.Errorf("self-host %s success = %v, want %v: %v\n%s", mode, runErr == nil, c.allowed, runErr, out)
				}
				if !c.allowed && runErr != nil && !strings.Contains(string(out), "attenuation") {
					t.Errorf("missing attenuation refusal: %s", out)
				}
			}
		})
	}
}
