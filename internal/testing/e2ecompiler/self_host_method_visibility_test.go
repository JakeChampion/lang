package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostMethodVisibilityFollowsImports(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	const entry = "import \"std/string\";\nimport \"./a\";\nfunction main(): i32 {\n  if (a.has_x(\"xyz\")) {\n    return 1;\n  }\n  return 0;\n}\n"
	const body = "pub function has_x(s: string): boolean {\n  return s.contains(\"x\");\n}\n"
	const arrayEntry = "import \"std/array\";\nimport \"./a\";\nfunction main(): i32 {\n  let xs: string[] = [\"a\"];\n  return a.f(xs);\n}\n"
	cases := []struct {
		name  string
		files map[string]string
		fails bool
		entry string
	}{
		{"sibling-import-does-not-reach", map[string]string{"a.fern": body}, true, ""},
		{"typo-without-import", map[string]string{"a.fern": "pub function has_x(s: string): boolean {\n  return s.contans(\"x\");\n}\n"}, true, ""},
		{"direct-import", map[string]string{"a.fern": "import \"std/string\";\n" + body}, false, ""},
		{"transitive-import", map[string]string{"a.fern": "import \"./b\";\n" + body, "b.fern": "import \"std/string\";\npub function one(): i32 { return 1; }\n"}, false, ""},
		{"array-sibling-import-does-not-reach", map[string]string{"a.fern": "pub function f(xs: string[]): i32 {\n  return xs.join(123).len();\n}\n"}, true, arrayEntry},
		{"array-near-miss-not-suggested", map[string]string{"a.fern": "pub function f(xs: string[]): i32 {\n  return xs.min_by_len().len();\n}\n"}, true, arrayEntry},
	}
	diag := regexp.MustCompile(`(?m)^[^\s:]*?([^/\s:]+\.fern):(\d+:\d+): error\[(\w+)\]: (.*)$`)
	diags := func(out []byte) []string {
		var ds []string
		for _, m := range diag.FindAllStringSubmatch(string(out), -1) {
			ds = append(ds, m[1]+":"+m[2]+" "+m[3]+" "+m[4])
		}
		return ds
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			main := entry
			if tc.entry != "" {
				main = tc.entry
			}
			files := map[string]string{"main.fern": main}
			for k, v := range tc.files {
				files[k] = v
			}
			for name, src := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, "main.fern")
			goOut, goErr := exec.Command(oracle, "-check", p).CombinedOutput()
			shOut, shErr := exec.Command(cli, "-check", p, root).CombinedOutput()
			want, got := diags(goOut), diags(shOut)
			if tc.fails != (len(want) > 0) || (goErr != nil) != tc.fails {
				t.Fatalf("Go checker: err %v, diagnostics %q; want failing=%t\n%s", goErr, want, tc.fails, goOut)
			}
			if (shErr != nil) != tc.fails || !slices.Equal(got, want) {
				t.Errorf("self-host (err %v):\n  %q\nGo checker:\n  %q\n%s", shErr, got, want, shOut)
			}
		})
	}
}
