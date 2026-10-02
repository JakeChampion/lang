package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostModloadUnresolvedImport pins #10880 on a loading driver
// (checker_modload_run, through the shared modloader): an import that resolves
// to no file is reported where it is resolved, naming the import and the file
// that wrote it, and the driver fails before checking. The one exception is a
// `std/` or `core/` import where no stdlib was given — no `std/` or `core/`
// tree beside the importer — which is skipped without a word, as the drivers'
// stdlib-free test projects rely on.
func TestSelfHostModloadUnresolvedImport(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	cases := []struct {
		name  string
		files map[string]string
		// want is the diagnostic, with DIR standing for the project directory;
		// empty means the run must be clean and quiet.
		want string
		// absent, when set, is all the case checks: the run never says it.
		absent string
	}{
		{
			name: "missing-from-entry",
			files: map[string]string{
				"main.fern": "import \"./helper\";\nfunction main(): i32 { return 0; }\n",
			},
			want: `checker_modload_run: cannot resolve import "./helper" in DIR/main.fern: DIR/helper.fern: not found`,
		},
		{
			name: "missing-from-imported-module",
			files: map[string]string{
				"main.fern": "import \"./mid\";\nfunction main(): i32 { return mid.f(); }\n",
				"mid.fern":  "import \"./gone\";\npub function f(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: cannot resolve import "./gone" in DIR/mid.fern: DIR/gone.fern: not found`,
		},
		{
			// A std/ tree is there, so the stdlib was given and a module
			// missing from it is a mistake, not an absent stdlib.
			name: "missing-from-given-stdlib",
			files: map[string]string{
				"main.fern":      "import \"std/nosuch\";\nfunction main(): i32 { return 0; }\n",
				"std/other.fern": "pub function x(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: cannot resolve import "std/nosuch" in DIR/main.fern: DIR/std/nosuch.fern: not found`,
		},
		{
			// An empty path is an import of no file, as any other is.
			name: "empty-path",
			files: map[string]string{
				"main.fern": "import \"\";\nfunction main(): i32 { return 0; }\n",
			},
			want: `checker_modload_run: cannot resolve import "" in DIR/main.fern: DIR/.fern: not found`,
		},
		{
			// A path the parser could not read is its diagnostic alone; the
			// loader is never asked to resolve it.
			name: "pathless-import",
			files: map[string]string{
				"main.fern": "import;\nfunction main(): i32 { return 0; }\n",
			},
			absent: "cannot resolve import",
		},
		{
			name: "no-stdlib-given-is-quiet",
			files: map[string]string{
				"main.fern": "import \"core/map\";\nimport \"std/io\";\n" +
					"function main(): i32 { let m: Map[string, i32] = map_new(2); m = m.insert(\"a\", 1); return m.len(); }\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var cmd *exec.Cmd
			entry := filepath.Join(dir, "main.fern")
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, entry)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin, entry)...)
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, _ := cmd.Output()
			code := cmd.ProcessState.ExitCode()
			got := strings.TrimRight(stderr.String(), "\n")
			if tc.absent != "" {
				if strings.Contains(got+string(out), tc.absent) {
					t.Fatalf("exit %d, stdout %q, stderr %q; want no %q", code, out, got, tc.absent)
				}
				return
			}
			if tc.want == "" {
				if code != 0 || got != "" || len(out) != 0 {
					t.Fatalf("exit %d, stdout %q, stderr %q; want a clean, quiet check", code, out, got)
				}
				return
			}
			if code != 2 {
				t.Errorf("exit %d, want 2 (the driver's load bail)", code)
			}
			if want := strings.ReplaceAll(tc.want, "DIR", dir); got != want {
				t.Errorf("stderr:\n%s\nwant:\n%s", got, want)
			}
			if len(out) != 0 {
				t.Errorf("driver checked past an unresolved import, stdout %q", out)
			}
		})
	}
}
