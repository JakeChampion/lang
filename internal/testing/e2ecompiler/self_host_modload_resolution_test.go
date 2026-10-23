package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostModloadResolvesBesideImporter pins the shared modloader's
// resolution to native's: an import other than `std/` and `core/` resolves
// beside the file that wrote it, not beside the entry, and a module is the
// file it resolves to, so two modules sharing a basename both load and each
// keeps its own namespace. Driven through checker_modload_run, whose checker
// rejects a reference bound to the wrong module.
func TestSelfHostModloadResolvesBesideImporter(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	cases := []struct {
		name  string
		files map[string]string
		// want is the diagnostic, with DIR standing for the project directory;
		// empty means the run must be clean and quiet.
		want string
	}{
		{
			name: "dot-import-inside-a-subdirectory",
			files: map[string]string{
				"main.fern":  "import \"./lib/a\";\nfunction main(): i32 { return a.f(); }\n",
				"lib/a.fern": "import \"./b\";\npub function f(): i32 { return b.g(); }\n",
				"lib/b.fern": "pub function g(): i32 { return 1; }\n",
			},
		},
		{
			name: "parent-import",
			files: map[string]string{
				"main.fern":        "import \"./app/mid\";\nfunction main(): i32 { return mid.f(); }\n",
				"app/mid.fern":     "import \"../shared/util\";\npub function f(): i32 { return util.h(); }\n",
				"shared/util.fern": "pub function h(): i32 { return 2; }\n",
			},
		},
		{
			name: "bare-sibling-import",
			files: map[string]string{
				"main.fern":       "import \"./app/mid\";\nfunction main(): i32 { return mid.f(); }\n",
				"app/mid.fern":    "import \"helper\";\npub function f(): i32 { return helper.h(); }\n",
				"app/helper.fern": "pub function h(): i32 { return 3; }\n",
			},
		},
		{
			name: "one-file-two-spellings",
			files: map[string]string{
				"main.fern":  "import \"./lib/a\";\nimport \"./lib/b\";\nfunction main(): i32 { return a.f() + b.g(); }\n",
				"lib/a.fern": "import \"../lib/b\";\npub function f(): i32 { return b.g(); }\n",
				"lib/b.fern": "pub function g(): i32 { return 1; }\n",
			},
		},
		{
			name: "same-basename-modules",
			files: map[string]string{
				"main.fern":   "import \"./a/util\" as ua;\nimport \"./b/util\" as ub;\nfunction main(): i32 { return ua.f() + ub.g(); }\n",
				"a/util.fern": "pub function f(): i32 { return 1; }\n",
				"b/util.fern": "pub function g(): i32 { return 2; }\n",
			},
		},
		{
			name: "stdlib-and-user-basename-collisions",
			files: map[string]string{
				"main.fern": `import "./frame";
import "std/http/h3/frame" as h3;
import "std/http/h2/frame" as h2;
function main(): i32 { let a: i32 = h2.value(); let b: string = h3.value(); let c: boolean = frame.value(); return a; }`,
				"frame.fern":             `pub function value(): boolean { return true; }`,
				"std/http/h2/frame.fern": `pub const PING: i32 = 1; pub function value(): i32 { return PING; }`,
				"std/http/h3/frame.fern": `pub const PING: i32 = 2; pub function value(): string { return "h3"; }`,
			},
		},
		{
			name: "missing-beside-importer",
			files: map[string]string{
				"main.fern":  "import \"./lib/a\";\nfunction main(): i32 { return a.f(); }\n",
				"lib/a.fern": "import \"./gone\";\npub function f(): i32 { return 1; }\n",
				"gone.fern":  "pub function x(): i32 { return 0; }\n",
			},
			want: `checker_modload_run: DIR/lib/a.fern:1:1: error[E086]: cannot resolve import "./gone": not found`,
		},
		{
			// A stdlib import resolves under the stdlib root and nowhere else:
			// a file of the same basename beside the importer is not it (#12690).
			name: "missing-stdlib-module-with-local-namesake",
			files: map[string]string{
				"main.fern":      "import \"std/other\";\n  import \"std/json2\";\nfunction main(): i32 { return other.x() + json2.y(); }\n",
				"json2.fern":     "pub function y(): i32 { return 1; }\n",
				"std/other.fern": "pub function x(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: DIR/main.fern:2:3: error[E086]: unknown stdlib module "std/json2"`,
		},
		{
			name: "missing-subpath-with-local-namesake",
			files: map[string]string{
				"main.fern": "import \"./sub/x\";\nfunction main(): i32 { return x.f(); }\n",
				"x.fern":    "pub function f(): i32 { return 1; }\n",
			},
			want: `checker_modload_run: DIR/main.fern:1:1: error[E086]: cannot resolve import "./sub/x": not found`,
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
			entry := filepath.Join(dir, "main.fern")
			var cmd *exec.Cmd
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
		})
	}
}
