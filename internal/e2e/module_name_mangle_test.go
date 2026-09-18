package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A module whose FILE NAME is not an identifier (#9684).
//
// The prefix a non-entry module's decls take is its basename, and it used to
// be used verbatim: `[.fern` produced the symbol `__fn_[__help_text`, which
// the in-process assembler refuses ("unsupported instruction") and gas refuses
// as "junk at end of line, first unrecognized character is `['".
//
// `coreutils/[.fern` — the bracket form of test(1), named for the program it
// is — has been in the tree since that utility landed and never tripped this,
// because it is only ever compiled as the ENTRY module and an entry module's
// prefix is "". The multi-call binary (#9671) is what imports it.
//
// THE HELPER HAS TO BE BIG ENOUGH TO SURVIVE INLINING. Three smaller
// reductions of this all built cleanly: a one-line private function is inlined
// away and its symbol never emitted, so the bug is invisible. Forty appends is
// comfortably over the threshold.
const moduleNameMangleBracket = `function help_text(): string {
    var out: string = "";
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

// An ALIAS is not optional here and never was: a bare qualifier `[.answer()`
// does not parse, so this is the only spelling that reaches the mangler at
// all.
const moduleNameMangleMain = `import "./[" as bracket;

function main(): i32 { return bracket.answer(); }
`

// 40 lines of 8 bytes each.
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

// Through the CLI rather than by lowering in-process, because the layer that
// rejected this is the ASSEMBLER: the mangler produces a string that looks
// fine, and only the encoder has an opinion about it. The default path is the
// in-process native backend, which is the one that failed.
func runModuleNameMangle(t *testing.T, target string, runner string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir := writeModuleNameMangleProject(t)
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", out, filepath.Join(dir, "main.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build for %s failed: %v\n%s", target, err, o)
	}
	var cmd *exec.Cmd
	if runner == "" {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(runner, out)
	}
	_, _ = cmd.CombinedOutput()
	if got := cmd.ProcessState.ExitCode(); got != moduleNameMangleWant {
		t.Errorf("exit code: got %d, want %d", got, moduleNameMangleWant)
	}
}

func TestModuleNameMangleX86_64(t *testing.T) {
	runModuleNameMangle(t, "x86-64-linux", "")
}

func TestModuleNameMangleArm64(t *testing.T) {
	runModuleNameMangle(t, "arm64-linux", arm64QemuOrEmpty(t))
}

// Two modules whose sanitised prefixes are the same string.
//
// Replacing what an assembler will not take with `_` is not injective:
// `[.fern` and `_.fern` both want the prefix `_`, as would `a-b.fern` beside
// `a_b.fern`. Sanitising BEFORE the collision loop rather than after is what
// keeps that from silently becoming one prefix and two definitions of every
// symbol — the loop that already disambiguates two modules sharing a basename
// disambiguates these too, and the emitted pair is `__fn____help_text` and
// `__fn___1__help_text`.
//
// Worth its own fixture because the ordinary one cannot fail this way: it has
// a single non-identifier module, so the replacement has nothing to collide
// with.
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

func runModuleNameMangleCollision(t *testing.T, target string, runner string) {
	t.Helper()
	fern := buildFernCLI(t)
	dir := writeModuleNameMangleCollisionProject(t)
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(fern, "-target", target, "-o", out, filepath.Join(dir, "main.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build for %s failed: %v\n%s", target, err, o)
	}
	var cmd *exec.Cmd
	if runner == "" {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(runner, out)
	}
	_, _ = cmd.CombinedOutput()
	// Both modules' answer(), so a prefix collision that lost one of the two
	// definitions would not reach this number even if it linked.
	if got, want := cmd.ProcessState.ExitCode(), 2*moduleNameMangleWant; got != want {
		t.Errorf("exit code: got %d, want %d", got, want)
	}
}

func TestModuleNameMangleCollisionX86_64(t *testing.T) {
	runModuleNameMangleCollision(t, "x86-64-linux", "")
}

func TestModuleNameMangleCollisionArm64(t *testing.T) {
	runModuleNameMangleCollision(t, "arm64-linux", arm64QemuOrEmpty(t))
}
