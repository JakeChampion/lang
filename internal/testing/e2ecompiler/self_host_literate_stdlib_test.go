package e2ecompiler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostStandardLibraryDoctests(t *testing.T) {
	cli := buildSelfHostCLI(t)
	examples := 0
	err := filepath.WalkDir(cli.stdlib, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".fern.md") {
			return err
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count := strings.Count(string(document), "```fern test")
		if count == 0 {
			return nil
		}
		examples += count
		rel, err := filepath.Rel(cli.stdlib, path)
		if err != nil {
			return err
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			cmd := runX86_64Bin(cli.runner, cli.bin, "-doctest", path, cli.stdlib)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.HasPrefix(string(output), fmt.Sprintf("1..%d\n", count)) || strings.Contains(string(output), "\nnot ok ") {
				t.Fatalf("doctests: %v\n%s", err, output)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if examples == 0 {
		t.Fatal("no standard-library doctests found")
	}
	t.Logf("checked %d standard-library examples", examples)
}

const literateStdlibProgram = `import "std/option";
import "std/result";
import "std/csv";
import "std/string";
function next(n: i32): i32 { return n + 1; }
function main(): i32 {
  let value: Option[i32] = Some(20);
  let result: Result[i32, string] = Ok(21);
  let Ok(fields) = csv.parse_line("a,\"b,c\",d") else { return 1; };
  if (fields.len() != 3 || fields[1] != "b,c") { return 2; }
  if (value.map(next).unwrap_or(0) + result.unwrap_or(0) != 42) { return 3; }
  return 42;
}`

func TestSelfHostLiterateStandardLibrary(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, module := range []struct{ name, documentation string }{
		{"option", "unwrap_or returns the value, or the supplied default for None."},
		{"result", "map_err transforms the error inside Err and retains Ok."},
		{"csv", "parse_line decodes one record, with one empty field for empty input."},
	} {
		t.Run(module.name, func(t *testing.T) {
			document := filepath.Join(cli.stdlib, "std", module.name+".fern.md")
			cmd := runX86_64Bin(cli.runner, cli.bin, "-doctest", document, cli.stdlib)
			if output, err := cmd.CombinedOutput(); err != nil || !strings.HasPrefix(string(output), "1..2\nok 1 - ") || !strings.Contains(string(output), "\nok 2 - ") {
				t.Fatalf("doctests: %v\n%s", err, output)
			}
			cmd = runX86_64Bin(cli.runner, cli.bin, "-doc", document)
			if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "title: std/"+module.name+"\n") || !strings.Contains(string(output), module.documentation) {
				t.Fatalf("API documentation: %v\n%s", err, output)
			}
		})
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, literateStdlibProgram, target); code != 42 {
				t.Fatalf("exit %d, want 42\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostPlaygroundLiterateStandardLibrary(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("requires a supported native host")
	}
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	bin := buildPlaygroundDriverForTarget(t, stdlib, target)
	for _, mode := range []string{"-check", "-interp"} {
		_, stderr, code := runPlayground(t, bin, t.TempDir(), literateStdlibProgram, mode)
		want := 0
		if mode == "-interp" {
			want = 42
		}
		if code != want {
			t.Fatalf("%s: exit %d, want %d\n%s", mode, code, want, stderr)
		}
	}
	wat, stderr, code := runPlayground(t, bin, t.TempDir(), literateStdlibProgram, "-emit", "asm")
	if code != 0 {
		t.Fatalf("compile: exit %d\n%s", code, stderr)
	}
	assertWatRuns(t, wat, 42)
}
