package e2eharness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportClosuresShareDependencies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		second    string
		roots     []string
		want      string
		wantError string
	}{
		{"overlap and repeated roots", "import \"./leaf\";", []string{"first.fern", "second.fern", "leaf.fern", "first.fern"}, "first.fern,leaf.fern,second.fern", ""},
		{"cycle", "import \"./first\"; import \"./second\";", []string{"second.fern", "first.fern"}, "second.fern,first.fern,leaf.fern", ""},
		{"missing later root", "", []string{"first.fern", "missing.fern"}, "", "missing.fern"},
		{"missing later dependency", "import \"./missing\";", []string{"first.fern", "second.fern"}, "", "missing.fern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeClosure(t, map[string]string{
				"first.fern":  "import \"./leaf\";",
				"second.fern": tc.second,
				"leaf.fern":   "function leaf(): i32 { return 1; }",
			})
			files, err := selfHostImportClosures(dir, tc.roots...)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %s", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, file := range files {
				names = append(names, filepath.Base(file))
			}
			if got := strings.Join(names, ","); got != tc.want {
				t.Fatalf("traversal = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCopySelfHostFilesPreservesSourceClosure(t *testing.T) {
	roots := []string{"asm_ir.fern", "parser.fern", "asm_arm64_ir.fern"}
	want := map[string][]byte{}
	for _, root := range roots {
		for _, file := range SelfHostImportClosure(t, selfHostSrcDir, root) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			want[filepath.Base(file)] = src
		}
	}
	dir := t.TempDir()
	CopySelfHostFiles(t, dir, roots...)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("staged %d files, want %d", len(entries), len(want))
	}
	for name, src := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, src) {
			t.Errorf("staged %s differs from its source", name)
		}
	}
}

func BenchmarkStageSelfHostAsmProject(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		WriteSelfHostAsmProject(b)
	}
}
