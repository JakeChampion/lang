package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Independent test workers may compile repository drivers from one directory.
// Each invocation must keep assembly and executables out of that directory.
func TestSelfHostCLIFileEntryIsolation(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	cases := []struct {
		name   string
		source string
		code   int
	}{
		{"first.fern", "function main(): i32 { return 11; }\n", 11},
		{"second.fern", "function main(): i32 { return 23; }\n", 23},
	}
	for _, tc := range cases {
		if err := os.WriteFile(filepath.Join(dir, tc.name), []byte(tc.source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("concurrent", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				stderr, code := cli.exitOfFile(t, filepath.Join(dir, tc.name), "arm64-linux", nil)
				if code != tc.code {
					t.Fatalf("file entry exited %d, want %d: %s", code, tc.code, stderr)
				}
			})
		}
	})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(cases) {
		t.Errorf("file-entry compilation wrote artifacts beside shared sources: %v", entries)
	}
	for _, tc := range cases {
		got, err := os.ReadFile(filepath.Join(dir, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.source {
			t.Errorf("file-entry compilation changed source %s", tc.name)
		}
	}
}
