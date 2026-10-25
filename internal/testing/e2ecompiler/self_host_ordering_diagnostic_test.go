package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostOrderingMismatchDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	diagnostic := regexp.MustCompile(`error\[(E\d+)\]: ([^\r\n]+)`)
	for _, operands := range [][2]string{{"1", "true"}, {"1.5", "true"}, {`"a"`, "1"}} {
		for _, op := range []string{"<", "<=", ">", ">="} {
			expr := operands[0] + " " + op + " " + operands[1]
			for _, tc := range []struct{ name, source, result string }{
				{"variable", "function main(): i32 { let x: i32 = " + expr + "; return 0; }", "E003"},
				{"return", "function main(): i32 { return " + expr + "; }", "E002"},
				{"const", "const WRONG: i32 = " + expr + "; function main(): i32 { return 0; }", "E002"},
			} {
				t.Run(fmt.Sprint(tc.name, "/", expr), func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
						t.Fatal(err)
					}
					var want []string
					for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
						out, err := exec.Command(frontend.bin, "-check", path, cli.stdlib).CombinedOutput()
						if err == nil {
							t.Fatalf("%s accepted invalid operands", frontend.name)
						}
						var got []string
						codes := map[string]int{}
						for _, match := range diagnostic.FindAllStringSubmatch(string(out), -1) {
							got = append(got, match[1]+"\t"+match[2])
							codes[match[1]]++
						}
						if len(got) != 2 || codes[tc.result] != 1 || codes["E009"] != 1 || !strings.Contains(string(out), "requires matching numeric or string operands") {
							t.Fatalf("%s: want operand and result errors:\n%s", frontend.name, out)
						}
						sort.Strings(got)
						if want == nil {
							want = got
						} else if !reflect.DeepEqual(got, want) {
							t.Errorf("diagnostics differ:\nGo: %q\nself: %q", want, got)
						}
					}
				})
			}
		}
	}
}
