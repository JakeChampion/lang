package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The wit_decode / wit_compose tests compile a driver program that imports the
// modules themselves — `import "./wit_decode";` next to a copy of the sources —
// through the self-host CLI, the way fern.fern links them, and run the result
// under wasmtime. The drivers are written unqualified (`wit_section_body(…)`);
// witQualify prefixes each module's pub names, so a driver reads as the plain
// call sequence it tests.

var (
	witCLIOnce   sync.Once
	witCLIBin    string
	witCLIStdlib string
	witCLIErr    string
	witCLISkip   string
)

// witSelfHostCLI builds compiler/fern.fern once per test binary as a
// binary this host runs directly, and returns it with the stdlib root.
func witSelfHostCLI(t *testing.T) (string, string) {
	t.Helper()
	witCLIOnce.Do(func() {
		cliTarget, _, skip := hostTargets()
		if skip != "" {
			witCLISkip = skip
			return
		}
		fern := buildLangBinForInterp(t)
		src, err := filepath.Abs("../../../compiler/fern.fern")
		if err != nil {
			witCLIErr = err.Error()
			return
		}
		stdlib, err := filepath.Abs("../../stdlib")
		if err != nil {
			witCLIErr = err.Error()
			return
		}
		dir, err := os.MkdirTemp("", "selfhost-wit-cli-")
		if err != nil {
			witCLIErr = err.Error()
			return
		}
		cli := filepath.Join(dir, "fern")
		if out, err := exec.Command(fern, "-target", cliTarget, "-o", cli, src).CombinedOutput(); err != nil {
			witCLIErr = fmt.Sprintf("building the self-host CLI for %s: %v\n%s", cliTarget, err, out)
			return
		}
		witCLIBin, witCLIStdlib = cli, stdlib
	})
	if witCLIErr != "" {
		t.Fatal(witCLIErr)
	}
	if witCLISkip != "" {
		t.Skip(witCLISkip)
	}
	return witCLIBin, witCLIStdlib
}

// witModules are the self-host modules a wit driver may import, in the order
// their import lines are written.
var witModules = []string{"watbin", "wit_decode", "wit_compose", "wit_proxy_world"}

// witPubNames returns the pub function and struct names of a self-host module.
func witPubNames(t *testing.T, module string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", module+".fern"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^pub (?:function|struct) (\w+)`).FindAllStringSubmatch(string(src), -1) {
		names = append(names, m[1])
	}
	return names
}

// witQualify prefixes every use of a wit module's pub name in driver with the
// module, and returns the import lines the driver needs.
func witQualify(t *testing.T, driver string) (string, string) {
	t.Helper()
	var imports strings.Builder
	for _, module := range witModules {
		used := false
		for _, name := range witPubNames(t, module) {
			re := regexp.MustCompile(`\b` + name + `\b`)
			if re.MatchString(driver) {
				driver = re.ReplaceAllString(driver, module+"."+name)
				used = true
			}
		}
		if used {
			fmt.Fprintf(&imports, "import \"./%s\";\n", module)
		}
	}
	return imports.String(), driver
}

// witCompileToWat writes driver (unqualified) as name.fern in dir beside a copy
// of the wit modules, compiles it with the self-host CLI to a preview-1 WAT
// module, and returns that module's path.
func witCompileToWat(t *testing.T, dir, name, driver string) string {
	t.Helper()
	cli, stdlib := witSelfHostCLI(t)
	files := make([]string, len(witModules))
	for i, m := range witModules {
		files[i] = m + ".fern"
	}
	copySelfHostFiles(t, dir, files...)
	imports, body := witQualify(t, driver)
	prog := filepath.Join(dir, name+".fern")
	if err := os.WriteFile(prog, []byte(imports+body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".wat")
	if msg, err := exec.Command(cli, "-target", "wasm32-wasi", "-emit", "asm", "-o", out, prog, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI on %s: %v\n%s", prog, err, msg)
	}
	return out
}
