package printer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/corpus"
)

func corpusRoot(t *testing.T) string {
	t.Helper()
	root, err := corpus.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := corpus.Materialize(root, dst, corpus.Sources); err != nil {
		t.Fatal(err)
	}
	return dst
}

func corpusFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := corpus.Files(root, corpus.Sources)
	if err != nil {
		t.Fatal(err)
	}
	shard := printerCorpusShard(t)
	selected := shard.files(files)
	if len(selected) == 0 {
		t.Fatalf("corpus shard %d/%d selected no files", shard.index, shard.count)
	}
	t.Logf("corpus shard %d/%d: %d of %d files", shard.index, shard.count, len(selected), len(files))
	return selected
}

func formatSource(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return Format(prog), nil
}

func TestFormatCorpusOutputTypeChecks(t *testing.T) {
	root := corpusRoot(t)
	files := corpusFiles(t, root)
	mirror := mirrorCorpus(t, root)

	checked := 0
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(mirror, rel)
		if typeChecksAt(path) != nil {
			continue
		}
		formatted, err := formatSource(string(src))
		if err != nil {
			t.Errorf("%s: parses in place but not through the formatter: %v", rel, err)
			continue
		}
		if err := os.WriteFile(path, []byte(formatted), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := typeChecksAt(path); err != nil {
			t.Errorf("%s: compiles as written, does not compile after -fmt: %v", rel, err)
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			t.Fatal(err)
		}
		checked++
	}
	t.Logf("checked %d formatted programs", checked)
	if checked < printerCorpusShard(t).minimumChecked() {
		t.Errorf("only %d of %d corpus files compiled as written; the gate is asserting far less than it looks like", checked, len(files))
	}
}

func TestFormatCorpusRoundtripShape(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		first, err := parser.Parse(string(src))
		if err != nil {
			continue
		}
		printed := Format(first)
		second, err := parser.Parse(printed)
		if err != nil {
			t.Errorf("%s: formatted output does not parse: %v", rel, err)
			continue
		}
		want, got := ASTShape(first), ASTShape(second)
		if want == got {
			continue
		}
		t.Errorf("%s: -fmt changed the AST\n%s", rel, FirstShapeDiff(want, got))
	}
}

func TestFormatCorpusIdempotent(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		once, err := formatSource(string(src))
		if err != nil {
			continue
		}
		twice, err := formatSource(once)
		if err != nil {
			t.Errorf("%s: formatted output does not parse: %v", rel, err)
			continue
		}
		if once != twice {
			t.Errorf("%s: -fmt is not idempotent\n%s", rel, FirstShapeDiff(once, twice))
		}
	}
}

func TestFormatCorpusPreservesCommentAnchor(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		got, err := formatSource(string(src))
		if err != nil {
			continue
		}
		want, have := commentAnchors(string(src)), commentAnchors(got)
		if len(want) != len(have) {
			t.Errorf("%s: comment count changed: %d -> %d", rel, len(want), len(have))
			continue
		}
		for i := range want {
			if want[i] != have[i] {
				t.Errorf("%s: comment %q attached to %q in the source, %q formatted", rel, want[i].text, want[i].anchor, have[i].anchor)
				break
			}
		}
	}
}

func TestFormatCorpusPreservesCommentBlanks(t *testing.T) {
	root := corpusRoot(t)
	for _, rel := range corpusFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		got, err := formatSource(string(src))
		if err != nil {
			continue
		}
		want, have := commentRunShapes(string(src)), commentRunShapes(got)
		if strings.Join(want, ",") != strings.Join(have, ",") {
			t.Errorf("%s: a blank line inside a comment run did not survive -fmt\n%s", rel, FirstShapeDiff(string(src), got))
		}
	}
}

func mirrorCorpus(t *testing.T, root string) string {
	t.Helper()
	dst := t.TempDir()
	for _, sub := range corpus.Sources {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func typeChecksAt(path string) error {
	prog, _, err := modload.Load(path)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}
