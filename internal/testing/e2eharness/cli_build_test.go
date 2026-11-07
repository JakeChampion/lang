package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIBuildKeyFollowsEachInput(t *testing.T) {
	work := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cli := filepath.Join(work, "cli")
	src := filepath.Join(work, "src")
	embed := filepath.Join(work, "embed")
	write(cli, "compiler one")
	write(filepath.Join(src, "main.fern"), "import \"./helper\";\nimport \"std/test\";\nfunction main(): i32 { return 0; }\n")
	write(filepath.Join(src, "helper.fern"), "pub function one(): i32 { return 1; }\n")
	write(filepath.Join(embed, "std", "a.fern"), "pub function a(): i32 { return 1; }\n")
	b := CLIBuild{Target: TargetX86_64Linux, Dir: src, Entry: "main.fern", Embed: embed}

	seen := map[string]string{}
	expect := func(change string, fresh bool) {
		t.Helper()
		k := cliBuildKey(t, cli, b)
		prev, ok := seen[k]
		if fresh && ok {
			t.Fatalf("%s: key %s is the key of %s", change, k, prev)
		}
		if !fresh && !ok {
			t.Fatalf("%s: the key changed", change)
		}
		seen[k] = change
	}
	expect("first build", true)
	expect("same inputs", false)
	write(cli, "compiler two")
	expect("new CLI bytes", true)
	write(filepath.Join(src, "helper.fern"), "pub function one(): i32 { return 2; }\n")
	expect("new imported source", true)
	write(filepath.Join(embed, "std", "a.fern"), "pub function a(): i32 { return 2; }\n")
	expect("new embedded file", true)
	b.Args = []string{"-emit", "core-module"}
	expect("new arguments", true)
	b.Target = TargetWasm32Wasi
	expect("new target", true)
	b.Env = []string{"FERN_SANITIZE=1"}
	expect("new FERN setting", true)
	t.Setenv("CLI_BUILD_KEY_UNRELATED", "1")
	expect("unrelated environment", false)
}
