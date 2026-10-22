package e2ecompiler

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
		grant    string // the -grant value, "" for none
	}{
		// The line goes directly under an existing table header, and the
		// comment and the dependency already there are untouched.
		{"path-into-existing-table", withTable, "helper", "path:../helper", true, ""},
		// A trailing comment on the header still names the table (#11841).
		{"path-into-commented-table", "[package]\nname = \"app\"\n[dependencies]  # deps\n", "helper", "path:../helper", true, ""},
		// No table yet: one is appended, with the same separating blank line.
		{"path-creates-table", noTable, "helper", "path:../helper", true, ""},
		// A manifest whose last line has no newline must not gain a joined line.
		{"path-no-trailing-newline", noTrailingNewline, "helper", "path:../helper", true, ""},
		{"workspace-spec", noTable, "member", "workspace", true, ""},
		{"name-with-punctuation", noTable, "my_dep-2", "path:../x", true, ""},
		// A version alone is the bare form; with a grant, an inline table.
		{"version-spec", noTable, "json", "version:1.2.0", true, ""},
		{"version-with-grant", noTable, "json", "version:1.2.0", true, "time,net"},
		{"path-with-grant", withTable, "kv", "path:../kv", true, "fs"},
		{"workspace-grant-none", noTable, "member", "workspace", true, "none"},
		// Refusals: each must be the same refusal, not merely a shared failure.
		{"already-declared", withTable, "existing", "path:../elsewhere", false, ""},
		{"version-not-semver", noTable, "json", "version:1.2", false, ""},
		{"grant-unknown-capability", noTable, "kv", "path:../kv", false, "net,teleport"},
		{"leading-digit-name", noTable, "9bad", "path:../x", false, ""},
		{"unrecognised-spec", noTable, "ok", "nonsense", false, ""},
		{"empty-path", noTable, "ok", "path:", false, ""},
		{"no-manifest", "", "ok", "path:../x", false, ""},
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

			args := func(dir string) []string {
				a := []string{"-add", c.dep, c.spec, dir}
				if c.grant != "" {
					a = append([]string{"-grant", c.grant}, a...)
				}
				return a
			}
			nativeCmd := exec.Command(nativeBin, args(nativeDir)...)
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, args(selfDir)...)
			shOut, _ := shCmd.CombinedOutput()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native add ok = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native add ok = %v, self-host = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
			nativeText := strings.ReplaceAll(strings.ReplaceAll(string(nativeOut), nativeDir, "<ROOT>"), "fern-oracle: ", "fern: ")
			if want, got := nativeText, strings.ReplaceAll(string(shOut), selfDir, "<ROOT>"); want != got {
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
	// Too few or too many arguments is a usage refusal: exit 2, as an
	// unknown flag is.
	t.Run("argument-count", func(t *testing.T) {
		for _, argv := range [][]string{{"-add", "helper"}, {"-add", "helper", "path:../h", t.TempDir(), "extra"}} {
			for _, bin := range []string{nativeBin, driverBin} {
				cmd := exec.Command(bin, argv...)
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != 2 {
					t.Errorf("%s %s exited %d, want 2", filepath.Base(bin), strings.Join(argv, " "), code)
				}
			}
		}
	})
	// A -grant after the arguments is a driver flag past the point flags are
	// parsed: both refuse it rather than write the dependency without it.
	t.Run("grant-after-arguments", func(t *testing.T) {
		for _, bin := range []string{nativeBin, driverBin} {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "fern.toml"), []byte(noTable), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "-add", "kv", "path:../kv", dir, "-grant", "net")
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != 2 || !strings.Contains(string(out), ": -grant comes after the source file") {
				t.Errorf("%s: exit %d, want 2 and the refusal\n%s", filepath.Base(bin), code, out)
			}
			if man, _ := os.ReadFile(filepath.Join(dir, "fern.toml")); string(man) != noTable {
				t.Errorf("%s: refused add still rewrote the manifest:\n%q", filepath.Base(bin), man)
			}
		}
	})
}

// TestSelfHostAddWritesEveryForm pins the line `-add` writes for each source
// and grant, so every dependency form the manifest accepts can be written
// without editing it by hand.
func TestSelfHostAddWritesEveryForm(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, c := range []struct{ spec, grant, want string }{
		{"path:../dep", "", `dep = { path = "../dep" }`},
		{"workspace", "", `dep = { workspace = true }`},
		{"version:1.2.0", "", `dep = "1.2.0"`},
		{"version:1.2.0", "time,net", `dep = { version = "1.2.0", capabilities = ["net", "time"] }`},
		{"path:../dep", "none", `dep = { path = "../dep", capabilities = [] }`},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fern.toml"), []byte("[package]\nname = \"app\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"-add", "dep", c.spec, dir}
		if c.grant != "" {
			args = append([]string{"-grant", c.grant}, args...)
		}
		out, err := runX86_64Bin(runner, bin, args...).CombinedOutput()
		if err != nil {
			t.Errorf("%s -grant %q: %v\n%s", c.spec, c.grant, err, out)
			continue
		}
		if want := "fern: added " + c.want + "\n"; string(out) != want {
			t.Errorf("%s -grant %q: printed %q, want %q", c.spec, c.grant, out, want)
		}
		man, err := os.ReadFile(filepath.Join(dir, "fern.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "[package]\nname = \"app\"\n\n[dependencies]\n" + c.want + "\n"; string(man) != want {
			t.Errorf("%s -grant %q: fern.toml\n%q\nwant\n%q", c.spec, c.grant, man, want)
		}
	}
	// A refusal says `fern:` and names the flag, as every other CLI refusal does.
	out, _ := runX86_64Bin(runner, bin, "-add", "9bad", "path:../x", t.TempDir()).CombinedOutput()
	if !strings.HasPrefix(string(out), "fern: -add: invalid dependency name") {
		t.Errorf("refusal %q does not start with \"fern: -add:\"", out)
	}
}
