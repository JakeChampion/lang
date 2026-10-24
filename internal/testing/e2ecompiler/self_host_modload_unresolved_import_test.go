package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostModloadUnresolvedImport(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	cases := []struct {
		name   string
		files  map[string]string
		want   string
		absent string
	}{
		{
			name: "missing-from-entry",
			files: map[string]string{
				"main.fern": "import \"./helper\";\nfunction main(): i32 { return 0; }\n",
			},
			want: `checker_modload_run: DIR/main.fern:1:1: error[E086]: cannot resolve import "./helper": not found`,
		},
		{
			name: "missing-from-imported-module",
			files: map[string]string{
				"main.fern": "import \"./mid\";\nfunction main(): i32 { return mid.f(); }\n",
				"mid.fern":  "import \"./gone\";\npub function f(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: DIR/mid.fern:1:1: error[E086]: cannot resolve import "./gone": not found`,
		},
		{
			name: "missing-from-given-stdlib",
			files: map[string]string{
				"main.fern":      "import \"std/nosuch\";\nfunction main(): i32 { return 0; }\n",
				"std/other.fern": "pub function x(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: DIR/main.fern:1:1: error[E086]: unknown stdlib module "std/nosuch"`,
		},
		{
			name: "empty-path",
			files: map[string]string{
				"main.fern": "import \"\";\nfunction main(): i32 { return 0; }\n",
			},
			want: `checker_modload_run: DIR/main.fern:1:1: error[E086]: cannot resolve import "": not found`,
		},
		{
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
