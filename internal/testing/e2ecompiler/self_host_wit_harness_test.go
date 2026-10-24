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

var (
	witCLIOnce   sync.Once
	witCLIBin    string
	witCLIStdlib string
	witCLIErr    string
	witCLISkip   string
)

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

var witModules = []string{"watbin", "wit_decode", "wit_compose", "wit_proxy_world"}

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
