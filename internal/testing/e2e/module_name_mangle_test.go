package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const moduleNameMangleBracket = `function help_text(): string {
    let out: string = "";
    out = out + "line 00\n"; out = out + "line 01\n"; out = out + "line 02\n";
    out = out + "line 03\n"; out = out + "line 04\n"; out = out + "line 05\n";
    out = out + "line 06\n"; out = out + "line 07\n"; out = out + "line 08\n";
    out = out + "line 09\n"; out = out + "line 10\n"; out = out + "line 11\n";
    out = out + "line 12\n"; out = out + "line 13\n"; out = out + "line 14\n";
    out = out + "line 15\n"; out = out + "line 16\n"; out = out + "line 17\n";
    out = out + "line 18\n"; out = out + "line 19\n"; out = out + "line 20\n";
    out = out + "line 21\n"; out = out + "line 22\n"; out = out + "line 23\n";
    out = out + "line 24\n"; out = out + "line 25\n"; out = out + "line 26\n";
    out = out + "line 27\n"; out = out + "line 28\n"; out = out + "line 29\n";
    out = out + "line 30\n"; out = out + "line 31\n"; out = out + "line 32\n";
    out = out + "line 33\n"; out = out + "line 34\n"; out = out + "line 35\n";
    out = out + "line 36\n"; out = out + "line 37\n"; out = out + "line 38\n";
    out = out + "line 39\n";
    return out;
}

pub function answer(): i32 { return help_text().len() / 8; }
`

const moduleNameMangleMain = `import "./[" as bracket;

function main(): i32 { return bracket.answer(); }
`

const moduleNameMangleWant = 40

func writeModuleNameMangleProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"[.fern":    moduleNameMangleBracket,
		"main.fern": moduleNameMangleMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func runModuleNameMangle(t *testing.T, target string, runner []string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir := writeModuleNameMangleProject(t)
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", out, filepath.Join(dir, "main.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build for %s failed: %v\n%s", target, err, o)
	}
	cmd := runUnder(runner, out)
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != moduleNameMangleWant {
		t.Errorf("exit code: got %d, want %d", got, moduleNameMangleWant)
	}
}

func runUnder(runner []string, prog string) *exec.Cmd {
	if len(runner) == 0 {
		return exec.Command(prog)
	}
	return exec.Command(runner[0], append(append([]string{}, runner[1:]...), prog)...)
}

func TestModuleNameMangleX86_64(t *testing.T) {
	runModuleNameMangle(t, "x86-64-linux", x86NativeRunner(t))
}

func TestModuleNameMangleArm64(t *testing.T) {
	runModuleNameMangle(t, "arm64-linux", arm64RunnerPrefix(t))
}

const moduleNameMangleCollisionMain = `import "./[" as bracket;
import "./_" as under;

function main(): i32 { return bracket.answer() + under.answer(); }
`

func writeModuleNameMangleCollisionProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"[.fern":    moduleNameMangleBracket,
		"_.fern":    moduleNameMangleBracket,
		"main.fern": moduleNameMangleCollisionMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func runModuleNameMangleCollision(t *testing.T, target string, runner []string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir := writeModuleNameMangleCollisionProject(t)
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", out, filepath.Join(dir, "main.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build for %s failed: %v\n%s", target, err, o)
	}
	cmd := runUnder(runner, out)
	_, _ = cmd.CombinedOutput()
	if got, want := cmd.ProcessState.ExitCode(), 2*moduleNameMangleWant; got != want {
		t.Errorf("exit code: got %d, want %d", got, want)
	}
}

func TestModuleNameMangleCollisionX86_64(t *testing.T) {
	runModuleNameMangleCollision(t, "x86-64-linux", x86NativeRunner(t))
}

func TestModuleNameMangleCollisionArm64(t *testing.T) {
	runModuleNameMangleCollision(t, "arm64-linux", arm64RunnerPrefix(t))
}

func arm64RunnerPrefix(t *testing.T) []string {
	t.Helper()
	if r := arm64QemuOrEmpty(t); r != "" {
		return []string{r}
	}
	return nil
}
