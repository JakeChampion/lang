package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/gates"
)

type fixtureSpec struct {
	name          string
	mainPath      string
	stdin         string
	wantOut       string
	contains      []string
	exact         bool
	wantExit      int
	backends      map[string]bool
	compileError  bool
	loweringError bool
	wantErrors    []string

	reclaimObservable bool
}

var allBackends = []string{"interp", "x86_64", "arm64", "wasm"}

func (f *fixtureSpec) rejectionCase() bool { return f.compileError || f.loweringError }

func loadFixture(t *testing.T, dir string) *fixtureSpec {
	t.Helper()
	f := &fixtureSpec{
		name:     filepath.Base(dir),
		mainPath: filepath.Join(dir, "main.fern"),
		exact:    true,
		backends: map[string]bool{},
	}

	if raw, ok := readOptionalFile(dir, "expected.error"); ok {
		f.compileError = true
		f.wantErrors = expectedDiagnosticLines(raw)
		if len(f.wantErrors) == 0 {
			t.Fatalf("%s: expected.error file is empty", f.name)
		}
		return f
	}

	if raw, ok := readOptionalFile(dir, "expected.lowering-error"); ok {
		f.loweringError = true
		f.wantErrors = expectedDiagnosticLines(raw)
		if len(f.wantErrors) == 0 {
			t.Fatalf("%s: expected.lowering-error file is empty", f.name)
		}
		return f
	}

	if raw, ok := readOptionalFile(dir, "stdin"); ok {
		f.stdin = raw
	}

	mode := strings.TrimSpace(readOptionalFileDefault(dir, "match", "exact"))
	if mode == "contains" {
		f.exact = false
	} else if mode != "exact" {
		t.Fatalf("%s: unknown match mode %q (want \"exact\" or \"contains\")", f.name, mode)
	}

	stdout, _ := readOptionalFile(dir, "expected.stdout")
	if f.exact {
		f.wantOut = stdout
	} else {
		for _, ln := range strings.Split(stdout, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				f.contains = append(f.contains, s)
			}
		}
	}

	exitStr := strings.TrimSpace(readOptionalFileDefault(dir, "expected.exit", "0"))
	n, err := strconv.Atoi(exitStr)
	if err != nil {
		t.Fatalf("%s: bad expected.exit %q: %v", f.name, exitStr, err)
	}
	f.wantExit = n

	if _, ok := readOptionalFile(dir, "reclaim-observable"); ok {
		f.reclaimObservable = true
	}

	if raw, ok := readOptionalFile(dir, "backends"); ok {
		for _, ln := range strings.Split(raw, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			for _, tok := range strings.Fields(ln) {
				f.backends[tok] = true
			}
		}
		for b := range f.backends {
			if !contains(allBackends, b) {
				t.Fatalf("%s: unknown backend %q in backends file", f.name, b)
			}
		}
		if len(f.backends) == 0 {
			t.Fatalf("%s: backends file names no backend, so the case would run on none of them "+
				"(delete the file to opt into all of %v)", f.name, allBackends)
		}
	} else {
		for _, b := range allBackends {
			f.backends[b] = true
		}
	}
	return f
}

func TestFernFixtures(t *testing.T) {
	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no fixtures found under %s", root)
	}

	for _, name := range names {
		dir, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "main.fern")); err != nil {
			t.Errorf("fixture %s has no main.fern", name)
			continue
		}
		f := loadFixture(t, dir)
		t.Run(name, func(t *testing.T) {
			if f.compileError {
				t.Run("check", func(t *testing.T) {
					errText, failed := runFixtureCompileError(f.mainPath)
					if !failed {
						t.Errorf("expected a compile error containing %q, but the program compiled cleanly", f.wantErrors[0])
						return
					}
					checkExpectedDiagnostics(t, "compile", f.wantErrors, errText)
				})
				return
			}
			if f.loweringError {
				for _, target := range []string{e2eharness.TargetWasm32Wasi, e2eharness.TargetX86_64Linux} {
					target := target
					t.Run("lower-"+target, func(t *testing.T) {
						errText, stage := runFixtureLoweringError(t, f.mainPath, target)
						switch stage {
						case loweringRejected:
							checkExpectedDiagnostics(t, "lowering", f.wantErrors, errText)
						case frontEndRejected:
							t.Errorf("the front end rejected this program, so it pins a front-end rule and belongs "+
								"in expected.error rather than expected.lowering-error\nfront-end error:\n%s", errText)
						case loweredCleanly:
							t.Errorf("expected lowering to fail with %q, but the program lowered cleanly", f.wantErrors[0])
						}
					})
				}
				return
			}
			if f.backends["interp"] {
				t.Run("interp", func(t *testing.T) {
					stdout, exit := runFixtureInterp(t, f.mainPath, f.stdin)
					f.check(t, stdout, exit)
				})
			}
		})
	}
}

func (f *fixtureSpec) check(t *testing.T, stdout string, exit int) {
	t.Helper()
	if f.exact {
		if stdout != f.wantOut {
			t.Errorf("stdout mismatch\n got: %q\nwant: %q", stdout, f.wantOut)
		}
	} else {
		for _, sub := range f.contains {
			if !strings.Contains(stdout, sub) {
				t.Errorf("stdout missing %q\nfull stdout:\n%s", sub, stdout)
			}
		}
	}
	if exit != f.wantExit {
		t.Errorf("exit = %d, want %d\nstdout:\n%s", exit, f.wantExit, stdout)
	}
}

func runFixtureArm64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	_, qemu := arm64Tooling(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetArm64Linux, mainPath, nil)
	return runBin(runArm64Bin(qemu, bin), stdin)
}

func runFixtureX86_64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, mainPath, nil)
	if len(runner) == 0 {
		return runBin(exec.Command(bin), stdin)
	}
	return runBin(exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...), stdin)
}

func runFixtureWasm(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	skipIfPreview2Missing(t)
	comp := filepath.Join(t.TempDir(), "prog.component.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, mainPath, comp).CombinedOutput(); err != nil {
		t.Fatalf("SELFHOST-COMPILE-FAIL -target wasm32-wasi %s: %v\n%s", mainPath, err, out)
	}
	stdout, _, ec := runWasmArtifact(t, comp, runOpts{stdin: stdin})
	return stdout, ec
}

func runFixtureCompileError(mainPath string) (string, bool) {
	render := func(err error) string {
		src, rerr := os.ReadFile(mainPath)
		if rerr != nil {
			return err.Error()
		}
		return diag.Format(mainPath, string(src), err)
	}
	if err := fixtureFrontEnd(mainPath); err != nil {
		return render(err), true
	}
	return "", false
}

func fixtureFrontEnd(mainPath string) error {
	prog, _, err := modload.Load(mainPath)
	if err != nil {
		return err
	}
	_, err = gates.Check(mainPath, prog, "", nil)
	return err
}

type loweringStage int

const (
	loweringRejected loweringStage = iota
	frontEndRejected
	loweredCleanly
)

func runFixtureLoweringError(t *testing.T, mainPath, target string) (string, loweringStage) {
	if err := fixtureFrontEnd(mainPath); err != nil {
		return err.Error(), frontEndRejected
	}
	args := []string{"-target", target, "-o", filepath.Join(t.TempDir(), "prog")}
	if target == e2eharness.TargetWasm32Wasi {
		args = append(args, "-emit", "core-module")
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), append(args, mainPath, e2eharness.SelfHostStdlibRoot(t))...)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if msg, err := cmd.CombinedOutput(); err != nil {
		return string(msg), loweringRejected
	}
	return "", loweredCleanly
}

func readOptionalFile(dir, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func readOptionalFileDefault(dir, name, def string) string {
	if s, ok := readOptionalFile(dir, name); ok {
		return s
	}
	return def
}

var diagCodeLine = regexp.MustCompile(`^.*: error\[(E\d+)\]: `)

func expectedDiagnosticLines(raw string) []string {
	var out []string
	for _, ln := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitDiagnostics(errText string) (codes []string, blocks []string) {
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, ln := range strings.Split(errText, "\n") {
		if m := diagCodeLine.FindStringSubmatch(ln); m != nil {
			flush()
			codes = append(codes, m[1])
		}
		if len(codes) > 0 {
			cur = append(cur, ln)
		}
	}
	flush()
	return codes, blocks
}

func checkExpectedDiagnostics(t *testing.T, stage string, want []string, errText string) {
	t.Helper()
	reportDiagnosticMismatches(stage, want, errText, t.Errorf)
}

func reportDiagnosticMismatches(stage string, want []string, errText string, failf func(format string, args ...any)) {
	codes, blocks := splitDiagnostics(errText)
	matched := map[string]bool{}
	for _, w := range want {
		hit := false
		for i, b := range blocks {
			if strings.Contains(b, w) {
				matched[codes[i]] = true
				hit = true
			}
		}
		if !hit && strings.Contains(errText, w) {
			hit = true
		}
		if !hit {
			failf("%s error does not contain %q\nfull error:\n%s", stage, w, errText)
		}
	}
	var unexpected []string
	seen := map[string]bool{}
	for _, c := range codes {
		if matched[c] || seen[c] {
			continue
		}
		seen[c] = true
		unexpected = append(unexpected, c)
	}
	if len(unexpected) > 0 {
		failf("%s reported %s that no line of the sidecar expects — either the case is "+
			"documenting something that does not work (which is what #9601 found in diag_e053), "+
			"or the cascade is legitimate and belongs in the sidecar, one line per diagnostic"+
			"\nfull error:\n%s",
			stage, strings.Join(unexpected, ", "), errText)
	}
}
