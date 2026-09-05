package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The bash completion std/cli generates has to be a script bash accepts, not
// just text that mentions the right words: a quoting slip in a help string
// would still pass the Fern-side substring checks and break the user's shell
// on `source`. `bash -n` parses without executing, so it is the one shell
// syntax check available on every runner (zsh and fish are not).
func TestCliBashCompletionParses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "comp.fern")
	prog := `import "std/cli" as cli;
function main(): i32 {
    var deploy: cli.CliSpec = cli.cli_new("deploy", "push the build");
    deploy = deploy.option("env", "e", "target environment (it's \"quoted\")");
    var s: cli.CliSpec = cli.cli_new("my-tool", "ship things");
    s = s.flag("verbose", "v", "explain what's happening");
    s = s.option("name", "", "who to ship as");
    s = s.command(deploy);
    match (s.completion("bash")) {
        Some(c) => { print(c); return 0; },
        None => { return 1; }
    }
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"_my_tool() {", "complete -o default -F _my_tool my-tool", "deploy) words='--env -e' ;;"} {
		if !strings.Contains(out, w) {
			t.Errorf("completion script missing %q:\n%s", w, out)
		}
	}

	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(out)
	if diag, err := check.CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected the generated completion: %v\n%s\nscript:\n%s", err, diag, out)
	}
}
