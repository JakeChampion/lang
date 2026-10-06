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
			name: "both rejection kinds at once",
			files: map[string]string{
				"main.fern":               "",
				"expected.error":          "E001",
				"expected.lowering-error": "E068",
			},
			want: "cannot be two stages",
		},
		{
			name: "lowering-error case with a run sidecar",
			files: map[string]string{
				"main.fern":               "",
				"expected.lowering-error": "E068",
				"expected.exit":           "1",
			},
			want: `also carries "expected.exit"`,
		},
		{
			name: "empty expected.lowering-error",
			files: map[string]string{
				"main.fern":               "",
				"expected.lowering-error": "  \n",
			},
			want: "expected.lowering-error is empty",
		},
		{
			name: "rejection case with a waiver to justify nothing",
			files: map[string]string{
				"main.fern":      "",
				"expected.error": "E001",
				"meta":           "waiver: harness-limit\nreason: n/a\n",
			},
			want: "asserts no output",
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
			files: map[string]string{"main.fern": "", "backends": "interp arm32", "meta": waiverOK},
			want:  `unknown backend "arm32"`,
		},
		{
			name:  "backends file selecting nothing",
			files: map[string]string{"main.fern": "", "backends": "# all of them?\n"},
			want:  "selects no backends",
		},
		{
			name:  "backends file selecting everything",
			files: map[string]string{"main.fern": "", "backends": "interp x86_64 arm64 wasm\n"},
			want:  "which is what omitting it means — delete it",
		},

		// The waiver rules.
		{
			name:  "contains mode with no waiver",
			files: map[string]string{"main.fern": "", "match": "contains"},
			want:  "no meta file saying why",
		},
		{
			name:  "backends subset with no waiver",
			files: map[string]string{"main.fern": "", "backends": "interp wasm"},
			want:  "no meta file saying why",
		},
		{
			name:  "meta with no waiver key",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "reason: because\n"},
			want:  "meta has no waiver:",
		},
		{
			name:  "unknown waiver kind",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: because-i-said-so\nreason: r\n"},
			want:  `unknown waiver "because-i-said-so"`,
		},
		{
			name:  "waiver on a case that does not weaken",
			files: map[string]string{"main.fern": "", "meta": waiverOK},
			want:  "already asserts byte-exact output on all four backends — delete the meta file",
		},
		{
			name:  "waiver with no reason",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: harness-limit\n"},
			want:  "has no reason:",
		},
		{
			name:  "implementation-gap with no issue",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: implementation-gap\nreason: r\n"},
			want:  "needs issue:",
		},
		{
			name:  "issue on a waiver that is not an implementation gap",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: harness-limit\nreason: r\nissue: 42\n"},
			want:  `only meaningful for waiver "implementation-gap"`,
		},
		{
			name:  "issue written with a leading hash",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: implementation-gap\nreason: r\nissue: #42\n"},
			want:  "takes a bare number",
		},
		{
			name:  "issue that is not a number",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: implementation-gap\nreason: r\nissue: soon\n"},
			want:  `issue "soon" is not a number`,
		},
		{
			name:  "unknown meta key",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: harness-limit\nreason: r\nowner: me\n"},
			want:  `unknown key "owner"`,
		},
		{
			name:  "duplicate meta key",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: harness-limit\nreason: r\nreason: also r\n"},
			want:  `duplicate key "reason"`,
		},
		{
			name:  "meta line that is not key: value",
			files: map[string]string{"main.fern": "", "match": "contains", "meta": "waiver: harness-limit\nreason: r\njust a sentence\n"},
			want:  "is not `key: value`",
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

// waiverOK is a complete, valid waiver — used both as the fixture for
// rules that need a case to be otherwise clean, and as the subject of
// the "waiver on a case that does not weaken" rejection.
const waiverOK = "waiver: harness-limit\nreason: the runner cannot observe this\n"

func TestCheckCaseFormatAccepts(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "fully asserting case, no meta",
			files: map[string]string{
				"main.fern":       "function main(): i32 { return 0; }",
				"helper.fern":     "",
				"expected.stdout": "hi\n",
				"expected.exit":   "0\n",
				"stdin":           "",
				"match":           "exact\n",
			},
		},
		{
			name: "lowering-error case",
			files: map[string]string{
				"main.fern":               "function main(): i32 { return 0; }",
				"expected.lowering-error": "E068\n",
			},
		},
		{
			name: "weakened case with a justified waiver",
			files: map[string]string{
				"main.fern": "function main(): i32 { return 0; }",
				"match":     "contains\n",
				"backends":  "# not on wasm yet\ninterp x86_64\n",
				"meta": "# why this asserts less\n" +
					"waiver: implementation-gap\n" +
					"issue: 2843\n" +
					"reason: the wasm backend has no sleep_ms yet,\n" +
					"  so this case cannot run there.\n",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems, err := checkCaseFormat(writeCaseDir(t, tc.files))
			if err != nil {
				t.Fatalf("checkCaseFormat: %v", err)
			}
			if len(problems) != 0 {
				t.Errorf("well-formed case reported problems: %v", problems)
			}
		})
	}
}

func TestParseMetaJoinsWrappedReasons(t *testing.T) {
	m, errs := parseMeta("waiver: harness-limit\nreason: first line\n  second line\n  third line\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := "first line second line third line"; m.reason != want {
		t.Errorf("reason = %q, want %q", m.reason, want)
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
