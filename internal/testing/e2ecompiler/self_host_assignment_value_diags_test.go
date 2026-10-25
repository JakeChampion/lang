package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostImmutableAssignmentValueDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct {
		name, source, refusal string
		mismatch              bool
	}{
		{"field", `struct S { p: i32 }
function main(): i32 { let s: S = S { p: 1 }; s.p = "x"; return 0; }`, "E048", true},
		{"index", `function main(): i32 { let xs: i32[] = [1]; xs[0] = "x"; return 0; }`, "E056", true},
		{"nested-field", `struct S { p: i32 } struct Outer { inner: S }
function main(): i32 { let s = Outer { inner: S { p: 1 } }; s.inner.p = false; return 0; }`, "E048", true},
		{"generic-field", `struct Box[T] { item: T }
function main(): i32 { let b: Box[i64] = Box[i64] { item: 1 }; b.item = "x"; return 0; }`, "E048", true},
		{"tuple-field", `function main(): i32 { let pair = (1, true); pair.0 = "x"; return 0; }`, "E048", true},
		{"field-literal-settlement", `struct S { p: i64 }
function main(): i32 { let s: S = S { p: 1 }; s.p = 2; return 0; }`, "E048", false},
		{"index-literal-settlement", `function main(): i32 { let xs: i64[] = [1]; xs[0] = 2; return 0; }`, "E056", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			var positions []string
			for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
				t.Run(frontend.name, func(t *testing.T) {
					cmd := exec.Command(frontend.bin, "-check", path, cli.stdlib)
					out, err := cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(out), "error["+tc.refusal+"]") {
						t.Fatalf("want %s refusal: %v\n%s", tc.refusal, err, out)
					}
					found := regexp.MustCompile(`([0-9]+:[0-9]+): error\[E003\]: assignment: expected ([^\r\n]+)`).FindAllStringSubmatch(string(out), -1)
					want := 0
					if tc.mismatch {
						want = 1
					}
					if len(found) != want || strings.Count(string(out), "error[E003]") != want {
						t.Fatalf("want %d assignment mismatch: %s", want, out)
					}
					if want != 0 {
						positions = append(positions, found[0][1]+" "+found[0][2])
					}
				})
			}
			if len(positions) == 2 && positions[0] != positions[1] {
				t.Errorf("frontends disagree: %q", positions)
			}
		})
	}
}
