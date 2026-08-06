// Negative coverage for the corpus format gate. The real corpus passes,
// so TestConformanceCorpusFormat alone cannot show the check would catch
// anything — these synthetic cases exercise each rejection.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCaseFormatRejections(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string // relative path → contents; "" for a directory
		want  string            // required substring of the single problem
	}{
		{
			name:  "typo'd sidecar",
			files: map[string]string{"main.fern": "", "expected.exitcode": "3"},
			want:  `unrecognised file "expected.exitcode"`,
		},
		{
			name:  "missing main.fern",
			files: map[string]string{"helper.fern": ""},
			want:  "no main.fern",
		},
		{
			name:  "subdirectory",
			files: map[string]string{"main.fern": "", "nested/": ""},
			want:  `subdirectory "nested"`,
		},
		{
			name:  "compile-error case with a run sidecar",
			files: map[string]string{"main.fern": "", "expected.error": "E001", "expected.stdout": "hi"},
			want:  `also carries "expected.stdout"`,
		},
		{
			name:  "empty expected.error",
			files: map[string]string{"main.fern": "", "expected.error": "  \n"},
			want:  "expected.error is empty",
		},
		{
			name:  "non-integer exit",
			files: map[string]string{"main.fern": "", "expected.exit": "yes"},
			want:  `expected.exit "yes" is not an integer`,
		},
		{
			name:  "out-of-range exit",
			files: map[string]string{"main.fern": "", "expected.exit": "300"},
			want:  "outside 0..255",
		},
		{
			name:  "bad match mode",
			files: map[string]string{"main.fern": "", "match": "regex"},
			want:  `match "regex"`,
		},
		{
			name:  "unknown backend",
			files: map[string]string{"main.fern": "", "backends": "interp arm32"},
			want:  `unknown backend "arm32"`,
		},
		{
			name:  "backends file selecting nothing",
			files: map[string]string{"main.fern": "", "backends": "# all of them?\n"},
			want:  "selects no backends",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeCaseDir(t, tc.files)
			problems, err := checkCaseFormat(dir)
			if err != nil {
				t.Fatalf("checkCaseFormat: %v", err)
			}
			if len(problems) != 1 {
				t.Fatalf("got %d problems, want 1: %v", len(problems), problems)
			}
			if !strings.Contains(problems[0], tc.want) {
				t.Errorf("problem %q does not mention %q", problems[0], tc.want)
			}
		})
	}
}

func TestCheckCaseFormatAcceptsAWellFormedCase(t *testing.T) {
	dir := writeCaseDir(t, map[string]string{
		"main.fern":       "function main(): i32 { return 0; }",
		"helper.fern":     "",
		"expected.stdout": "hi\n",
		"expected.exit":   "0\n",
		"stdin":           "",
		"match":           "contains\n",
		"backends":        "# not on wasm yet\ninterp x86_64\n",
	})
	problems, err := checkCaseFormat(dir)
	if err != nil {
		t.Fatalf("checkCaseFormat: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("well-formed case reported problems: %v", problems)
	}
}

func writeCaseDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if strings.HasSuffix(name, "/") {
			if err := os.Mkdir(filepath.Join(dir, strings.TrimSuffix(name, "/")), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
