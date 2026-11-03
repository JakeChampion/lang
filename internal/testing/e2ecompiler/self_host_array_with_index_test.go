package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostArrayWithIndexDiagnostics(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	oracle := buildLangBinForInterp(t)
	cases := []struct{ name, source, want string }{
		{"i64", `let k: i64 = 1; let xs = [4, 5].with(k, 9);`, "i64"},
		{"u32", `let k: u32 = 1u32; let xs = [4, 5].with(k, 9);`, "u32"},
		{"u64", `let k: u64 = 1u64; let xs = [4, 5].with(k, 9);`, "u64"},
		{"boolean", `let xs = [4, 5].with(true, 9);`, "boolean"},
		{"string", `let xs = [4, 5].with("1", 9);`, "string"},
		{"inferred-wide-sum", `let b = 0; let v: i64 = 8; let n = v >> b; let off: i32 = 0; let xs = [4, 5].with(off + b, 9);`, "i64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(entry, []byte("function main(): i32 { "+tc.source+" return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`error[E038]: argument 1 to "with": expected i32, got %s`, tc.want)
			commands := []*exec.Cmd{exec.Command(oracle, "-check", entry)}
			for _, mode := range []string{"-check", "-interp"} {
				commands = append(commands, exec.Command(cli, mode, entry, stdlib))
			}
			for _, target := range selfHostFusionTargets {
				commands = append(commands, exec.Command(cli, "-target", target, "-o", filepath.Join(t.TempDir(), "out"), entry, stdlib))
			}
			for _, cmd := range commands {
				if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), want) {
					t.Errorf("%v: %v\n%s\nwant %s", cmd.Args, err, out, want)
				}
			}
		})
	}
}

func TestSelfHostArrayWithNarrowIndex(t *testing.T) {
	entry := e2eharness.RepoPath("conformance", "cases", "array_with_narrow_index", "main.fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, cli := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
		if out, err := exec.Command(cli, "-interp", entry, stdlib).CombinedOutput(); err != nil || string(out) != "1 7 3\n" {
			t.Fatalf("%s -interp: %v\n%s", cli, err, out)
		}
	}
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostFile(t, target, entry, nil)
			if out, err := runScaleTarget(t, target, bin).CombinedOutput(); err != nil || string(out) != "1 7 3\n" {
				t.Fatalf("%s: %v\n%s", target, err, out)
			}
		})
	}
}
