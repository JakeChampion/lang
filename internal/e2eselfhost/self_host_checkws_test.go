package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkStatusLines reduces `-check <DIR>` output to its per-member verdicts and
// the summary, dropping the diagnostics that explain a failure.
//
// The verdicts are what the two compilers must agree on. The explanations are
// not comparable and are not meant to be: the self-host checker is partial
// (#4346), so a member both refuse can be refused for differently-worded
// reasons — and the two even order the text differently, native printing FAIL
// before the diagnostic it collected, the self-host printing diagnostics as it
// finds them and the verdict after.
func checkStatusLines(out string) string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "ok   "), strings.HasPrefix(line, "FAIL "),
			strings.HasPrefix(line, "check: "):
			rows = append(rows, line)
		}
	}
	return strings.Join(rows, "\n")
}

// TestSelfHostCheckWorkspaceDifferentialX86_64 is the parity gate for
// `-check <DIR>` (#6640): the self-host must visit the same packages, reach
// the same verdict on each, and summarise the workspace the same way native
// does.
//
// A workspace-wide check is the command that answers "does this multi-package
// program build" — so a self-host that silently checks fewer members than
// native reports a green workspace nobody validated.
func TestSelfHostCheckWorkspaceDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("workspace check differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	const okLib = "pub function f(): i32 { return 1; }\n"
	// Ill-typed in a way BOTH checkers catch: the self-host's is partial, so a
	// member has to fail for a reason it models too, or the differential is
	// pinning native's coverage rather than the workspace walk.
	const badLib = "pub function f(): i32 { return \"nope\"; }\n"

	for _, c := range []struct {
		name   string
		files  map[string]string
		target string // the directory passed to -check, relative to the tree
		wantOK bool
	}{
		// Every member checks clean: both report one `ok` per member and the
		// same count.
		{"workspace-all-ok", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"b\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  okLib,
			"b/fern.toml": "[package]\nname = \"b\"\n",
			"b/main.fern": "function main(): i32 { return 0; }\n",
		}, ".", true},
		// One broken member does not hide the rest: the walk continues, and
		// the summary counts what failed.
		{"workspace-one-fails", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"b\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  okLib,
			"b/fern.toml": "[package]\nname = \"b\"\n",
			"b/lib.fern":  badLib,
		}, ".", false},
		// A member with neither its `lib` module nor main.fern is a per-member
		// failure naming both spellings, not a crash.
		{"workspace-member-without-entry", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"gone\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  okLib,
			// `gone` has a manifest and no module.
			"gone/fern.toml": "[package]\nname = \"gone\"\n",
		}, ".", false},
		// A plain package directory (no [workspace]) checks its own entry
		// module, and says nothing about members.
		{"package-dir", map[string]string{
			"fern.toml": "[package]\nname = \"solo\"\n",
			"lib.fern":  okLib,
		}, ".", true},
		// The manifest's `lib` key selects the entry module.
		{"package-dir-custom-lib", map[string]string{
			"fern.toml": "[package]\nname = \"solo\"\nlib = \"core.fern\"\n",
			"core.fern": okLib,
		}, ".", true},
		// A package whose manifest names a module that is not there is
		// refused, rather than checking nothing and reporting success.
		{"package-dir-no-entry", map[string]string{
			"fern.toml":  "[package]\nname = \"solo\"\n",
			"other.fern": okLib,
		}, ".", false},
		// A directory inside a package resolves upward to that package.
		{"nested-dir-resolves-upward", map[string]string{
			"fern.toml":    "[package]\nname = \"solo\"\n",
			"lib.fern":     okLib,
			"sub/keep.txt": "",
		}, "sub", true},
		// A directory no manifest governs is refused by both.
		{"no-manifest", map[string]string{
			"empty/keep.txt": "",
		}, "empty", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativeDir := filepath.Join(root, "native")
			selfDir := filepath.Join(root, "selfhost")
			for _, d := range []string{nativeDir, selfDir} {
				writeResolveProject(t, d, c.files)
			}

			nativeCmd := exec.Command(nativeBin, "-check", filepath.Join(nativeDir, c.target))
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-check", filepath.Join(selfDir, c.target))
			shOut, _ := shCmd.CombinedOutput()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native check ok = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native check ok = %v, self-host = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
			want := checkStatusLines(strings.ReplaceAll(string(nativeOut), nativeDir, "<ROOT>"))
			got := checkStatusLines(strings.ReplaceAll(string(shOut), selfDir, "<ROOT>"))
			if want != got {
				t.Errorf("member verdicts differ:\n--- native ---\n%s\n--- self-host ---\n%s\n\n--- native raw ---\n%s\n--- self-host raw ---\n%s",
					want, got, nativeOut, shOut)
			}
		})
	}
}
