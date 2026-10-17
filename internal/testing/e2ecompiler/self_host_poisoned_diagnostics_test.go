package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// These cases mirror the Go checker's poisoned-binding corpus: an invalid
// initializer must not turn later uses into more errors, while independent
// mistakes and redeclarations remain visible.
func TestSelfHostPoisonedBindingDiagnostics(t *testing.T) {
	data, err := os.ReadFile(e2eharness.RepoPath("internal", "testing", "e2ecompiler", "testdata", "poisoned_diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Source string
		Codes        []string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 16 {
		t.Fatalf("only %d poisoned-binding cases", len(cases))
	}
	selfhost := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	code := regexp.MustCompile(`error\[([EP][0-9]{3})\]`)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"main.fern": c.Source})
			path := filepath.Join(dir, "main.fern")
			for _, compiler := range []struct {
				Name, Bin string
				Args      []string
			}{
				{"selfhost", selfhost, []string{"-check", path, e2eharness.SelfHostStdlibRoot(t)}},
				{"go-oracle", oracle, []string{"-check", path}},
			} {
				text, accepted := checkVerdict(exec.Command(compiler.Bin, compiler.Args...))
				var got []string
				for _, m := range code.FindAllStringSubmatch(text, -1) {
					got = append(got, m[1])
				}
				sort.Strings(got)
				if accepted || !reflect.DeepEqual(got, c.Codes) {
					t.Fatalf("%s codes %v, want %v:\n%s", compiler.Name, got, c.Codes, text)
				}
			}
		})
	}
}
