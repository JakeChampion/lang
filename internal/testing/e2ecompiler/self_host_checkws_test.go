package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	const badLib = "pub function f(): i32 { return \"nope\"; }\n"

	for _, c := range []struct {
		name   string
		files  map[string]string
		target string
		wantOK bool
	}{
		{"workspace-all-ok", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"b\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  okLib,
			"b/fern.toml": "[package]\nname = \"b\"\n",
			"b/main.fern": "function main(): i32 { return 0; }\n",
		}, ".", true},
		{"workspace-one-fails", map[string]string{
			"fern.toml":   "[workspace]\nmembers = [\"a\", \"b\"]\n",
			"a/fern.toml": "[package]\nname = \"a\"\n",
			"a/lib.fern":  okLib,
			"b/fern.toml": "[package]\nname = \"b\"\n",
			"b/lib.fern":  badLib,
		}, ".", false},
		{"workspace-member-without-entry", map[string]string{
			"fern.toml":      "[workspace]\nmembers = [\"a\", \"gone\"]\n",
			"a/fern.toml":    "[package]\nname = \"a\"\n",
			"a/lib.fern":     okLib,
			"gone/fern.toml": "[package]\nname = \"gone\"\n",
		}, ".", false},
		{"package-dir", map[string]string{
			"fern.toml": "[package]\nname = \"solo\"\n",
			"lib.fern":  okLib,
		}, ".", true},
		{"package-dir-custom-lib", map[string]string{
			"fern.toml": "[package]\nname = \"solo\"\nlib = \"core.fern\"\n",
			"core.fern": okLib,
		}, ".", true},
		{"package-dir-no-entry", map[string]string{
			"fern.toml":  "[package]\nname = \"solo\"\n",
			"other.fern": okLib,
		}, ".", false},
		{"nested-dir-resolves-upward", map[string]string{
			"fern.toml":    "[package]\nname = \"solo\"\n",
			"lib.fern":     okLib,
			"sub/keep.txt": "",
		}, "sub", true},
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

func TestSelfHostCheckDirRefusesUnknownTargetX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{
		"fern.toml":   "[workspace]\nmembers = [\"a\"]\n",
		"a/fern.toml": "[package]\nname = \"a\"\n",
		"a/main.fern": "function main(): i32 {\n  return 0;\n}\n",
	})
	for _, arg := range []string{ws, filepath.Join(ws, "a", "main.fern")} {
		out, err := runX86_64Bin(runner, driver, "-check", "-target", "bogus", arg).CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != 2 || !strings.Contains(string(out), "unknown -target: bogus") || strings.Contains(string(out), "ok   a") {
			t.Errorf("-check -target bogus %s: exit %d\n%s\nwant exit 2 refusing the target before checking anything", arg, code, out)
		}
	}
}
