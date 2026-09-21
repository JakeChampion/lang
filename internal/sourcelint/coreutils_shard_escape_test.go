package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The coreutils lane joins derived names into a `-run` alternation, and a
// utility name is not a Go identifier: coreutils/[.fern is in the catalogue.
// A bare `[` opens an unterminated character class, so the shard holding it
// exits at regexp parse having run NOTHING — red start to finish rather than
// quietly wrong, and the reaper then cancels the rest of the run.
//
// This runs the lane's own escaper over every name the catalogue actually
// holds plus a metacharacter sweep, and holds it to regexp.QuoteMeta: the
// escaped name must compile, and must still match the name it came from.
func TestCoreutilsShardEscaperHandlesEveryMetacharacter(t *testing.T) {
	src := workflowSource(t, "test-coreutils.yml")
	var escaper string
	for _, line := range strings.Split(src, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "re_escape()") {
			escaper = s[strings.Index(s, "{")+1 : strings.LastIndex(s, "}")]
			break
		}
	}
	if escaper == "" {
		t.Fatal("test-coreutils.yml defines no re_escape function — if the lane's shape changed, update this gate with it")
	}

	names := []string{"[", "]", ".", "*", "^", "$", "(", ")", "{", "}", "?", "+", "|", "\\", "cat", "a.b"}
	root := filepath.Join("..", "..")
	utils, err := filepath.Glob(filepath.Join(root, "coreutils", "*.fern"))
	if err != nil {
		t.Fatalf("glob coreutils: %v", err)
	}
	if len(utils) == 0 {
		t.Fatal("no coreutils/*.fern found — if the catalogue moved, update this gate with it")
	}
	for _, u := range utils {
		names = append(names, strings.TrimSuffix(filepath.Base(u), ".fern"))
	}

	cmd := exec.Command("bash", "-c", "re_escape() {"+escaper+"}; re_escape")
	cmd.Stdin = strings.NewReader(strings.Join(names, "\n") + "\n")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the lane's re_escape: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(got) != len(names) {
		t.Fatalf("re_escape returned %d lines for %d names", len(got), len(names))
	}
	for i, name := range names {
		if got[i] != regexp.QuoteMeta(name) {
			t.Errorf("re_escape(%q) = %q, regexp.QuoteMeta gives %q", name, got[i], regexp.QuoteMeta(name))
			continue
		}
		re, err := regexp.Compile("^(" + got[i] + ")$")
		if err != nil {
			t.Errorf("re_escape(%q) = %q, which does not compile in an alternation: %v", name, got[i], err)
			continue
		}
		if !re.MatchString(name) {
			t.Errorf("re_escape(%q) = %q, which no longer matches the name it came from", name, got[i])
		}
	}
}
