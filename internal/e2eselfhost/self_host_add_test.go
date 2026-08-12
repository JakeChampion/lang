package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAddDifferentialX86_64 is the parity gate for `-add` (#6640):
// given the same manifest and the same dependency spec, native and the
// self-host must produce the same manifest bytes, or refuse for the same
// reason.
//
// The manifest is edited textually by both, so this pins more than the
// dependency line: where the line lands relative to an existing table, that
// comments and blank lines survive, and that a manifest with no
// `[dependencies]` table grows one in the same shape.
//
// The `url:` spec is deliberately absent. Recording an archive's sha256 means
// downloading it, which the self-host driver has no fetcher for and which a
// test must not do — the self-host refuses it with a diagnostic instead, and
// that refusal has no native counterpart to compare against offline.
func TestSelfHostAddDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("add differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	const withTable = "# my package\n[package]\nname = \"app\"\n\n[dependencies]\nexisting = { path = \"../existing\" }\n"
	const noTable = "[package]\nname = \"app\"\n"
	const noTrailingNewline = "[package]\nname = \"app\""

	for _, c := range []struct {
		name     string
		manifest string // "" = no manifest at all
		dep      string
		spec     string
		wantOK   bool
	}{
		// The line goes directly under an existing table header, and the
		// comment and the dependency already there are untouched.
		{"path-into-existing-table", withTable, "helper", "path:../helper", true},
		// No table yet: one is appended, with the same separating blank line.
		{"path-creates-table", noTable, "helper", "path:../helper", true},
		// A manifest whose last line has no newline must not gain a joined line.
		{"path-no-trailing-newline", noTrailingNewline, "helper", "path:../helper", true},
		{"workspace-spec", noTable, "member", "workspace", true},
		{"name-with-punctuation", noTable, "my_dep-2", "path:../x", true},
		// Refusals: each must be the same refusal, not merely a shared failure.
		{"already-declared", withTable, "existing", "path:../elsewhere", false},
		{"leading-digit-name", noTable, "9bad", "path:../x", false},
		{"unrecognised-spec", noTable, "ok", "nonsense", false},
		{"empty-path", noTable, "ok", "path:", false},
		{"no-manifest", "", "ok", "path:../x", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativeDir := filepath.Join(root, "native")
			selfDir := filepath.Join(root, "selfhost")
			for _, d := range []string{nativeDir, selfDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
				if c.manifest == "" {
					continue
				}
				if err := os.WriteFile(filepath.Join(d, "fern.toml"), []byte(c.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			nativeCmd := exec.Command(nativeBin, "-add", c.dep, c.spec, nativeDir)
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-add", c.dep, c.spec, selfDir)
			shOut, _ := shCmd.CombinedOutput()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native add ok = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native add ok = %v, self-host = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
			if want, got := strings.ReplaceAll(string(nativeOut), nativeDir, "<ROOT>"),
				strings.ReplaceAll(string(shOut), selfDir, "<ROOT>"); want != got {
				t.Errorf("add output differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}
			if c.manifest == "" {
				return
			}
			nativeMan, err := os.ReadFile(filepath.Join(nativeDir, "fern.toml"))
			if err != nil {
				t.Fatalf("native manifest: %v", err)
			}
			shMan, err := os.ReadFile(filepath.Join(selfDir, "fern.toml"))
			if err != nil {
				t.Fatalf("self-host manifest: %v", err)
			}
			if string(nativeMan) != string(shMan) {
				t.Errorf("fern.toml differs:\n--- native ---\n%q\n--- self-host ---\n%q", nativeMan, shMan)
			}
			// A refused add must leave the manifest exactly as it was — a
			// half-applied edit is worse than no edit.
			if !c.wantOK && string(shMan) != c.manifest {
				t.Errorf("refused add still rewrote the manifest:\n%q", shMan)
			}
		})
	}
}
