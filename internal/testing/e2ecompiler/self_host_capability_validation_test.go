package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestSelfHostCapabilityGrantValidation(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-cap-validation")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, value string
		valid       bool
	}{
		{"string", `"env"`, false},
		{"integer", `1`, false},
		{"boolean", `true`, false},
		{"table", `{}`, false},
		{"mixed-array", `["env", 1]`, false},
		{"unquoted-name", `[env]`, false},
		{"missing-comma", `["env" "fs"]`, false},
		{"escaped-name", `["en\v"]`, false},
		{"unknown-name", `["fss"]`, false},
		{"unclosed-array", `["env"`, false},
		{"empty", `[]`, true},
		{"known", `["env", "fs"]`, true},
		{"duplicates", `["env", "env"]`, true},
		{"trailing-comma", `["env",]`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj := t.TempDir()
			entry := writeCapsProject(t, proj, `helper = { path = "../helper", capabilities = `+c.value+` }`, "pub function save(s: string): i32 { return s.len(); }\n")
			_, _, oracleErr := modload.Load(entry)
			if (oracleErr == nil) != c.valid {
				t.Fatalf("Go loader success = %v, want %v: %v", oracleErr == nil, c.valid, oracleErr)
			}
			for _, mode := range []string{"-check", "-capabilities", "compile"} {
				t.Run(mode, func(t *testing.T) {
					args := []string{mode, entry, stdlib}
					if mode == "compile" {
						args = []string{"-target", "x86-64-linux", "-o", filepath.Join(proj, "out"), entry, stdlib}
					}
					out, runErr := exec.Command(cli, args...).CombinedOutput()
					if (runErr == nil) != c.valid {
						t.Errorf("success = %v, want %v: %v\n%s", runErr == nil, c.valid, runErr, out)
					}
					if !c.valid && (!strings.Contains(string(out), "fern.toml") || !strings.Contains(string(out), "capabilities")) {
						t.Errorf("missing manifest capability diagnostic: %s", out)
					}
				})
			}
		})
	}
}

func TestSelfHostCapabilityGrantKeyRecognition(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capability projects use native filesystem paths")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern-cap-validation")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range []string{
		`path = "../capabilities/[env]"`,
		`capabilities = ["env"], path = "../capabilities/[env]"`,
	} {
		t.Run(fields, func(t *testing.T) {
			entry := writeAttenuationProject(t, map[string]string{
				"app/main.fern":                "import \"helper\";\nfunction main(): i32 { return helper.one(); }\n",
				"app/fern.toml":                "[package]\nname = \"app\"\n[dependencies]\nhelper = { " + fields + " }\n",
				"capabilities/[env]/fern.toml": "[package]\nname = \"helper\"\n",
				"capabilities/[env]/lib.fern":  "pub function one(): i32 { let x = env(\"FERN_CAP_REPRO\"); return 0; }\n",
			})
			if _, _, err := modload.Load(entry); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(cli, "-check", entry, stdlib).CombinedOutput()
			if err != nil {
				t.Fatalf("valid path and grant rejected: %v\n%s", err, out)
			}
		})
	}
}
